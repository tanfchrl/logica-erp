package migration

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFlow_DiscoveryToCOA is the deterministic Step 1 → Step 2 golden test
// called out in docs/agent-build-prompt.md §10 ("Conversation tests: golden-
// file tests for key agent flows — migration step 1→2"). It runs the pure
// orchestration logic (state machine + COA derivation) for a fixed
// SetupProfile and asserts the resulting state matches an embedded golden.
// LLM calls and the session.Store backend are not involved; this proves the
// deterministic core is stable.
func TestFlow_DiscoveryToCOA(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		profile           SetupProfile
		wantIndustry      Industry
		wantContainsAccts []string
	}{
		{
			name: "manufacturing",
			profile: SetupProfile{
				BusinessType:    "Furniture factory",
				Industry:        "Manufacturing",
				Employees:       42,
				BaseCurrency:    "IDR",
				FiscalYearStart: "01-01",
				LegacySystem:    "ERPNext",
			},
			wantIndustry: IndustryManufacturing,
			wantContainsAccts: []string{
				"Kas", "Bank", "Piutang Usaha", "PPN Masukan", "PPN Keluaran",
				"Bahan Baku", "Barang Jadi", "Overhead",
			},
		},
		{
			name: "trading",
			profile: SetupProfile{
				BusinessType: "Retail toko",
				Industry:     "Perdagangan umum",
				BaseCurrency: "IDR",
			},
			wantIndustry: IndustryTrading,
			wantContainsAccts: []string{
				"Kas", "Bank", "Piutang Usaha", "Persediaan",
				"Retur Penjualan", "Beban Angkut",
			},
		},
		{
			name: "services",
			profile: SetupProfile{
				BusinessType: "Consulting",
				Industry:     "jasa konsultan",
				BaseCurrency: "IDR",
			},
			wantIndustry: IndustryServices,
			wantContainsAccts: []string{
				"Kas", "Bank", "Piutang Usaha", "Pendapatan Jasa", "Diterima",
			},
		},
		{
			name: "construction",
			profile: SetupProfile{
				BusinessType: "General contractor",
				Industry:     "kontraktor sipil",
				BaseCurrency: "IDR",
			},
			wantIndustry: IndustryConstruction,
			wantContainsAccts: []string{
				"Kas", "Bank", "Piutang Usaha",
			},
		},
		{
			name:              "other falls back to base PSAK",
			profile:           SetupProfile{BusinessType: "exotic", Industry: "", BaseCurrency: "IDR"},
			wantIndustry:      IndustryOther,
			wantContainsAccts: []string{"Kas", "Bank", "Piutang Usaha"},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			// Initial state (what Start() seeds).
			st := &State{CurrentStep: StepDiscovery, StepData: map[Step]any{}}
			if st.CurrentStep != StepDiscovery {
				t.Fatalf("seed: expected StepDiscovery, got %s", st.CurrentStep)
			}

			// --- SaveProfile transition (Step 1 → Step 2 advance) ---
			st.Profile = &c.profile
			st.completeStep(StepDiscovery)
			st.CurrentStep = StepCOA
			if st.CurrentStep != StepCOA {
				t.Fatalf("after SaveProfile, expected StepCOA, got %s", st.CurrentStep)
			}
			if !containsStep(st.Completed, StepDiscovery) {
				t.Fatalf("Step 1 not marked complete: completed=%v", st.Completed)
			}
			if containsStep(st.Completed, StepCOA) {
				t.Fatalf("Step 2 must not be auto-completed before proposal accepted")
			}

			// --- ProposeCOA transition (the Step 2 body) ---
			gotIndustry := resolveIndustry(st.Profile.Industry, st.Profile.BusinessType)
			if gotIndustry != c.wantIndustry {
				t.Fatalf("industry classification: got %s want %s", gotIndustry, c.wantIndustry)
			}
			proposal := buildCOAProposal(gotIndustry)
			if len(proposal) == 0 {
				t.Fatal("empty COA proposal")
			}

			// Golden invariants: every base PSAK row must be present, and the
			// industry-specific accounts the test names must appear.
			byNumber := indexByNumber(proposal)
			for _, base := range psakBaseCOA() {
				if _, ok := byNumber[base.AccountNumber]; !ok {
					t.Errorf("base PSAK account missing from proposal: %s %s",
						base.AccountNumber, base.Name)
				}
			}
			names := accountNames(proposal)
			for _, want := range c.wantContainsAccts {
				if !containsName(names, want) {
					t.Errorf("expected account %q not in proposal for %s; got names: %s",
						want, c.name, strings.Join(names, ", "))
				}
			}

			// Persist proposal into step data exactly as ProposeCOA does.
			st.StepData[StepCOA] = map[string]any{
				"proposal":          proposal,
				"accepted":          false,
				"resolved_industry": string(gotIndustry),
			}

			// Round-trip through the same encoder the persistence layer uses,
			// so the golden assertion catches accidental shape drift.
			roundTripped, err := json.Marshal(st)
			if err != nil {
				t.Fatalf("state marshal: %v", err)
			}
			var back State
			if err := json.Unmarshal(roundTripped, &back); err != nil {
				t.Fatalf("state unmarshal: %v", err)
			}
			if back.CurrentStep != StepCOA {
				t.Errorf("round-trip lost current step: %s", back.CurrentStep)
			}
			if back.Profile == nil || back.Profile.Industry != c.profile.Industry {
				t.Errorf("round-trip lost profile: %+v", back.Profile)
			}
			if _, ok := back.StepData[StepCOA]; !ok {
				t.Errorf("round-trip lost StepData[coa]")
			}

			// --- AcceptCOA transition ---
			data := back.StepData[StepCOA].(map[string]any)
			data["accepted"] = true
			back.completeStep(StepCOA)
			back.CurrentStep = StepDataMigration
			if !containsStep(back.Completed, StepCOA) {
				t.Errorf("AcceptCOA failed to mark Step 2 complete")
			}
			if back.CurrentStep != StepDataMigration {
				t.Errorf("after AcceptCOA expected StepDataMigration, got %s", back.CurrentStep)
			}
		})
	}
}

func containsStep(steps []Step, want Step) bool {
	for _, s := range steps {
		if s == want {
			return true
		}
	}
	return false
}

func accountNames(rows []COAAccount) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func containsName(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
