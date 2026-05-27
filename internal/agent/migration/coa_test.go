package migration

import (
	"strings"
	"testing"
)

func TestResolveIndustry(t *testing.T) {
	t.Parallel()
	type tc struct {
		name, industry, businessType string
		want                         Industry
	}
	cases := []tc{
		{"english trading", "Retail", "Sells goods", IndustryTrading},
		{"indonesian dagang", "perdagangan umum", "", IndustryTrading},
		{"english manufacturing", "Manufacturing", "Factory", IndustryManufacturing},
		{"indonesian pabrik", "pabrik furnitur", "", IndustryManufacturing},
		{"english services", "Consulting agency", "", IndustryServices},
		{"indonesian jasa", "jasa cleaning", "", IndustryServices},
		{"english construction", "Construction", "General contractor", IndustryConstruction},
		{"indonesian kontraktor", "kontraktor sipil", "", IndustryConstruction},
		// Disambiguation: "construction services" must classify as construction,
		// not services, because the more specific term wins.
		{"construction services", "construction services", "", IndustryConstruction},
		{"empty falls back", "", "", IndustryOther},
		{"unknown falls back", "blockchain DAO", "web3 stuff", IndustryOther},
		// Free-text business_type can carry the hint when industry is empty.
		{"business_type fallback", "", "manufaktur tekstil", IndustryManufacturing},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveIndustry(c.industry, c.businessType); got != c.want {
				t.Errorf("resolveIndustry(%q, %q) = %s, want %s",
					c.industry, c.businessType, got, c.want)
			}
		})
	}
}

func TestBuildCOAProposal_OtherIsBaseUnchanged(t *testing.T) {
	t.Parallel()
	base := psakBaseCOA()
	got := buildCOAProposal(IndustryOther)
	if len(got) != len(base) {
		t.Fatalf("IndustryOther should be base-only; got %d rows want %d", len(got), len(base))
	}
	for i, a := range base {
		if got[i] != a {
			t.Errorf("row %d differs: base=%+v overlay=%+v", i, a, got[i])
		}
	}
}

// Every overlay must produce a valid COA: every Parent must resolve to a
// row in the same proposal, no duplicate account numbers, and the merged
// COA must still cover the five PSAK roots.
func TestBuildCOAProposal_AllIndustriesAreInternallyConsistent(t *testing.T) {
	t.Parallel()
	for _, ind := range []Industry{
		IndustryTrading, IndustryManufacturing,
		IndustryServices, IndustryConstruction, IndustryOther,
	} {
		ind := ind
		t.Run(string(ind), func(t *testing.T) {
			t.Parallel()
			rows := buildCOAProposal(ind)

			// Unique account numbers.
			seen := map[string]bool{}
			for _, a := range rows {
				if seen[a.AccountNumber] {
					t.Errorf("duplicate account_number %s in %s overlay", a.AccountNumber, ind)
				}
				seen[a.AccountNumber] = true
			}

			// Every Parent reference points to a row in the same proposal.
			for _, a := range rows {
				if a.Parent == "" {
					continue
				}
				if !seen[a.Parent] {
					t.Errorf("%s overlay: account %s has unresolved parent %s",
						ind, a.AccountNumber, a.Parent)
				}
			}

			// All five PSAK roots must still be covered.
			roots := map[string]bool{}
			for _, a := range rows {
				roots[a.RootType] = true
			}
			for _, r := range []string{"asset", "liability", "equity", "income", "expense"} {
				if !roots[r] {
					t.Errorf("%s overlay drops root_type %s", ind, r)
				}
			}
		})
	}
}

func TestBuildCOAProposal_ManufacturingHasWIP(t *testing.T) {
	t.Parallel()
	rows := buildCOAProposal(IndustryManufacturing)
	wantNumbers := []string{"1141", "1142", "1143", "5110", "5120", "5130"}
	have := indexByNumber(rows)
	for _, n := range wantNumbers {
		if _, ok := have[n]; !ok {
			t.Errorf("manufacturing overlay missing %s", n)
		}
	}
	// Persediaan (1140) was a leaf in base — overlay must flip it to group.
	if !have["1140"].IsGroup {
		t.Error("manufacturing overlay should convert 1140 Persediaan to a group")
	}
	// And the new child must point at it.
	if have["1142"].Parent != "1140" {
		t.Errorf("WIP parent should be 1140, got %s", have["1142"].Parent)
	}
	// WIP label must mention "Dalam Proses" (this is the affordance reviewers
	// look for — strip that and the customer rebuilds it by hand).
	if !strings.Contains(have["1142"].Name, "Dalam Proses") {
		t.Errorf("1142 should be the WIP account, got name %q", have["1142"].Name)
	}
}

func TestBuildCOAProposal_TradingHasReturnsAndFreight(t *testing.T) {
	t.Parallel()
	rows := buildCOAProposal(IndustryTrading)
	have := indexByNumber(rows)
	for _, n := range []string{"1141", "4150", "4160", "5210", "5220"} {
		if _, ok := have[n]; !ok {
			t.Errorf("trading overlay missing %s", n)
		}
	}
	if !have["1140"].IsGroup {
		t.Error("trading overlay should convert 1140 Persediaan to a group")
	}
	if have["4150"].RootType != "income" {
		t.Errorf("retur penjualan must classify as income (contra-revenue), got %s",
			have["4150"].RootType)
	}
}

func TestBuildCOAProposal_ServicesHasDeferredRevenue(t *testing.T) {
	t.Parallel()
	rows := buildCOAProposal(IndustryServices)
	have := indexByNumber(rows)
	// Unbilled AR, service WIP, deferred revenue — the three services staples.
	for _, n := range []string{"1135", "1145", "2170"} {
		if _, ok := have[n]; !ok {
			t.Errorf("services overlay missing %s", n)
		}
	}
	if have["2170"].RootType != "liability" {
		t.Errorf("pendapatan diterima di muka must be a liability, got %s",
			have["2170"].RootType)
	}
	// Revenue head renamed from "Penjualan" → "Pendapatan Jasa".
	if !strings.Contains(have["4100"].Name, "Jasa") {
		t.Errorf("services overlay should rename 4100 to Pendapatan Jasa, got %q",
			have["4100"].Name)
	}
}

func TestBuildCOAProposal_ConstructionHasProgressBillingsAndRetention(t *testing.T) {
	t.Parallel()
	rows := buildCOAProposal(IndustryConstruction)
	have := indexByNumber(rows)
	for _, n := range []string{"1145", "2170", "2180", "5110"} {
		if _, ok := have[n]; !ok {
			t.Errorf("construction overlay missing %s", n)
		}
	}
	// Retensi is a liability — not a contra-asset, because the cash hasn't
	// been received yet on retention amounts withheld.
	if have["2180"].RootType != "liability" {
		t.Errorf("utang retensi must be a liability, got %s", have["2180"].RootType)
	}
	if !strings.Contains(have["4100"].Name, "Kontrak") {
		t.Errorf("construction overlay should rename 4100 to Pendapatan Kontrak, got %q",
			have["4100"].Name)
	}
}

func indexByNumber(rows []COAAccount) map[string]COAAccount {
	out := make(map[string]COAAccount, len(rows))
	for _, a := range rows {
		out[a.AccountNumber] = a
	}
	return out
}
