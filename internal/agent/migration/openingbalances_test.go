package migration

import (
	"strings"
	"testing"
)

// fixtureCOA: a small COA-by-account-number lookup that the tests share.
// Matches the shape of what listOrEmpty returns from /accounting/accounts.
func fixtureCOA() map[string]map[string]any {
	return map[string]map[string]any{
		"1110": {"id": "acc_kas",     "name": "Kas",            "account_type": "cash",       "account_number": "1110"},
		"1120": {"id": "acc_bank",    "name": "Bank",           "account_type": "bank",       "account_number": "1120"},
		"1130": {"id": "acc_ar",      "name": "Piutang Usaha",  "account_type": "receivable", "account_number": "1130"},
		"1140": {"id": "acc_stock",   "name": "Persediaan",     "account_type": "stock",      "account_number": "1140"},
		"2110": {"id": "acc_ap",      "name": "Utang Usaha",    "account_type": "payable",    "account_number": "2110"},
		"3100": {"id": "acc_equity",  "name": "Modal Disetor",  "account_type": "equity",     "account_number": "3100"},
	}
}

func TestBuildProposal_Balanced(t *testing.T) {
	t.Parallel()
	lines := []OpeningBalanceLine{
		{AccountNumber: "1110", Debit: "1000000", Credit: "0"},
		{AccountNumber: "1130", Debit: "500000",  Credit: "0"},
		{AccountNumber: "3100", Debit: "0",       Credit: "1500000"},
	}
	prop, _, _, err := buildProposal(lines, fixtureCOA(), "2026-01-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prop.Balanced {
		t.Errorf("expected Balanced=true, got false (D=%s C=%s)", prop.TotalDebit, prop.TotalCredit)
	}
	if prop.TotalDebit != "1500000" || prop.TotalCredit != "1500000" {
		t.Errorf("totals: want 1500000/1500000, got %s/%s", prop.TotalDebit, prop.TotalCredit)
	}
	if prop.Imbalance != "" {
		t.Errorf("balanced proposal must omit Imbalance, got %q", prop.Imbalance)
	}
	if len(prop.UnmappedAccounts) != 0 {
		t.Errorf("expected no unmapped, got %v", prop.UnmappedAccounts)
	}
	// All three lines should have resolved_account_id set.
	for _, l := range prop.Lines {
		if l.ResolvedAccountID == "" {
			t.Errorf("line %s: ResolvedAccountID should be set", l.AccountNumber)
		}
	}
}

func TestBuildProposal_Unbalanced(t *testing.T) {
	t.Parallel()
	// 1M debit, 900K credit → imbalance 100K.
	lines := []OpeningBalanceLine{
		{AccountNumber: "1110", Debit: "1000000", Credit: "0"},
		{AccountNumber: "3100", Debit: "0",       Credit: "900000"},
	}
	prop, _, _, err := buildProposal(lines, fixtureCOA(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prop.Balanced {
		t.Fatal("expected Balanced=false")
	}
	if prop.Imbalance != "100000" {
		t.Errorf("Imbalance: want 100000, got %s", prop.Imbalance)
	}
}

func TestBuildProposal_RejectsNegative(t *testing.T) {
	t.Parallel()
	cases := [][]OpeningBalanceLine{
		{{AccountNumber: "1110", Debit: "-100", Credit: "0"}},
		{{AccountNumber: "1110", Debit: "0",    Credit: "-100"}},
	}
	for i, lines := range cases {
		_, _, _, err := buildProposal(lines, fixtureCOA(), "")
		if err == nil {
			t.Errorf("case %d: expected error on negative amount, got nil", i)
		} else if !strings.Contains(err.Error(), "non-negative") {
			t.Errorf("case %d: error should mention non-negative, got %q", i, err.Error())
		}
	}
}

func TestBuildProposal_RejectsDualSide(t *testing.T) {
	t.Parallel()
	// A single line that has both a debit AND a credit is meaningless —
	// reconciliation can't tell which side of the GL it represents.
	lines := []OpeningBalanceLine{
		{AccountNumber: "1110", Debit: "1000", Credit: "500"},
	}
	_, _, _, err := buildProposal(lines, fixtureCOA(), "")
	if err == nil {
		t.Fatal("expected error on dual-sided line")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Errorf("error should mention 'both', got %q", err.Error())
	}
}

func TestBuildProposal_RejectsBadDecimal(t *testing.T) {
	t.Parallel()
	lines := []OpeningBalanceLine{
		{AccountNumber: "1110", Debit: "totally-not-a-number", Credit: "0"},
	}
	_, _, _, err := buildProposal(lines, fixtureCOA(), "")
	if err == nil {
		t.Fatal("expected error on malformed decimal")
	}
	if !strings.Contains(err.Error(), "1110") {
		t.Errorf("error should name the offending account, got %q", err.Error())
	}
}

func TestBuildProposal_EmptyAmountsTreatedAsZero(t *testing.T) {
	t.Parallel()
	// CSV imports often have blank cells where 0 is meant. Whitespace-only
	// values should resolve to zero, not an error.
	lines := []OpeningBalanceLine{
		{AccountNumber: "1110", Debit: "1000", Credit: "   "},
		{AccountNumber: "3100", Debit: "",      Credit: "1000"},
	}
	prop, _, _, err := buildProposal(lines, fixtureCOA(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prop.Balanced {
		t.Errorf("blank-as-zero proposal should still balance: D=%s C=%s", prop.TotalDebit, prop.TotalCredit)
	}
}

func TestBuildProposal_UnmappedAccounts(t *testing.T) {
	t.Parallel()
	// Two lines reference numbers absent from the COA.
	lines := []OpeningBalanceLine{
		{AccountNumber: "1110", Debit: "1000", Credit: "0"},
		{AccountNumber: "9999", Debit: "0",    Credit: "1000"},  // unknown
		{AccountNumber: "8888", Debit: "500",  Credit: "0"},     // unknown
	}
	prop, _, _, err := buildProposal(lines, fixtureCOA(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prop.UnmappedAccounts) != 2 {
		t.Errorf("expected 2 unmapped, got %d: %v", len(prop.UnmappedAccounts), prop.UnmappedAccounts)
	}
	// Order in the list mirrors input order — important so the FE shows the
	// rows in the same order as the CSV.
	if prop.UnmappedAccounts[0] != "9999" || prop.UnmappedAccounts[1] != "8888" {
		t.Errorf("unmapped order: want [9999 8888], got %v", prop.UnmappedAccounts)
	}
}

func TestBuildProposal_InventoryLineSurfaced(t *testing.T) {
	t.Parallel()
	// account_type='stock' on the resolved row marks the Inventory line so
	// the stock-reconciliation overlay can compare TB ↔ stock ledger.
	lines := []OpeningBalanceLine{
		{AccountNumber: "1140", Debit: "750000", Credit: "0"},  // Persediaan → stock
		{AccountNumber: "3100", Debit: "0",      Credit: "750000"},
	}
	_, invLine, invAcct, err := buildProposal(lines, fixtureCOA(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invLine == nil {
		t.Fatal("expected an inventory line to be surfaced")
	}
	if invLine.AccountNumber != "1140" || invLine.Debit != "750000" {
		t.Errorf("inventory line wrong: %+v", invLine)
	}
	if invAcct == nil || invAcct["id"] != "acc_stock" {
		t.Errorf("inventory account wrong: %+v", invAcct)
	}
}

func TestBuildProposal_NoInventoryWhenAbsent(t *testing.T) {
	t.Parallel()
	// A trial balance without an account_type='stock' line should return
	// nil for both inventory pointers so the caller skips the stock proof.
	lines := []OpeningBalanceLine{
		{AccountNumber: "1110", Debit: "1000", Credit: "0"},
		{AccountNumber: "3100", Debit: "0",    Credit: "1000"},
	}
	_, invLine, invAcct, err := buildProposal(lines, fixtureCOA(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invLine != nil {
		t.Errorf("expected no inventory line, got %+v", invLine)
	}
	if invAcct != nil {
		t.Errorf("expected no inventory account, got %+v", invAcct)
	}
}

func TestBuildProposal_PreservesLineOrder(t *testing.T) {
	t.Parallel()
	// Lines come out in the order the user uploaded them. Important so the
	// FE can map a row error back to the CSV row number.
	lines := []OpeningBalanceLine{
		{AccountNumber: "3100", Debit: "0",    Credit: "300"},
		{AccountNumber: "1110", Debit: "100",  Credit: "0"},
		{AccountNumber: "1130", Debit: "200",  Credit: "0"},
	}
	prop, _, _, err := buildProposal(lines, fixtureCOA(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"3100", "1110", "1130"}
	for i, l := range prop.Lines {
		if l.AccountNumber != wantOrder[i] {
			t.Errorf("line %d: want %s, got %s", i, wantOrder[i], l.AccountNumber)
		}
	}
}

func TestBuildProposal_TrimWhitespaceOnAccountNumber(t *testing.T) {
	t.Parallel()
	// CSV exports often pad numbers with whitespace. They must still match
	// the COA lookup.
	lines := []OpeningBalanceLine{
		{AccountNumber: " 1110 ", Debit: "100", Credit: "0"},
	}
	prop, _, _, err := buildProposal(lines, fixtureCOA(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prop.UnmappedAccounts) != 0 {
		t.Errorf("whitespace-padded number should still map: %v", prop.UnmappedAccounts)
	}
	if prop.Lines[0].ResolvedAccountID != "acc_kas" {
		t.Errorf("resolved id wrong: %v", prop.Lines[0])
	}
}

func TestBuildProposal_DecimalPrecision(t *testing.T) {
	t.Parallel()
	// IDR is integer-only in practice but the wire format is decimal. Verify
	// fractional values balance correctly — important for SQL-driven trial
	// balances that may include .00 suffixes.
	lines := []OpeningBalanceLine{
		{AccountNumber: "1110", Debit: "1234567.89", Credit: "0"},
		{AccountNumber: "3100", Debit: "0",          Credit: "1234567.89"},
	}
	prop, _, _, err := buildProposal(lines, fixtureCOA(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prop.Balanced {
		t.Errorf("expected fractional balanced TB to be balanced: D=%s C=%s", prop.TotalDebit, prop.TotalCredit)
	}
}
