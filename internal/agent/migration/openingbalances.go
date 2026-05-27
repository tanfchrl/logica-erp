package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/agent/erpclient"
)

// OpeningBalanceLine is one row of the trial balance the user provides
// (typically exported from the legacy system). After ProposeOpeningBalances
// resolves account_number → account_id, the line is ready to be turned into
// a Journal Entry row.
type OpeningBalanceLine struct {
	AccountNumber string `json:"account_number"`
	LegacyLabel   string `json:"legacy_label,omitempty"`
	Debit         string `json:"debit"`
	Credit        string `json:"credit"`

	// Populated by the proposer:
	ResolvedAccountID   string `json:"resolved_account_id,omitempty"`
	ResolvedAccountName string `json:"resolved_account_name,omitempty"`
	AccountType         string `json:"account_type,omitempty"` // for stock-recon check
}

// OpeningBalanceProposal is the structured proof + draft payload the FE
// renders on Step 4. `Balanced` and `StockCheck.Matches` (when present) are
// the two reconciliation gates; submit must be blocked until both pass.
type OpeningBalanceProposal struct {
	Lines            []OpeningBalanceLine `json:"lines"`
	TotalDebit       string               `json:"total_debit"`
	TotalCredit      string               `json:"total_credit"`
	Balanced         bool                 `json:"balanced"`
	Imbalance        string               `json:"imbalance,omitempty"`
	UnmappedAccounts []string             `json:"unmapped_accounts,omitempty"`
	StockCheck       *StockReconciliation `json:"stock_check,omitempty"`
	// PostingDate is what the resulting JE will use. Defaults to today.
	PostingDate string `json:"posting_date"`
}

// StockReconciliation surfaces the second proof from spec §4 Step 4:
// *"opening stock value matches the Inventory account balance in the same JE"*.
//
// Skipped (StockCheck == nil) when no Inventory account exists in the COA
// or when the trial balance has no entry against it.
type StockReconciliation struct {
	InventoryAccountID string `json:"inventory_account_id"`
	InventoryAccountNo string `json:"inventory_account_no"`
	TrialBalanceDebit  string `json:"trial_balance_debit"`
	// StockLedgerTotal is the sum of valuation_value across stock_ledger
	// rows known to the ERP, fetched via the read API. Compared to the
	// trial-balance debit for the Inventory account.
	StockLedgerTotal string `json:"stock_ledger_total"`
	Matches          bool   `json:"matches"`
	Difference       string `json:"difference,omitempty"`
}

// ProposeOpeningBalances resolves every legacy account_number against the
// caller's current COA and runs the reconciliation proofs. The result lives
// on agent_session.state["step_data"]["opening_balances"] so it can be
// re-read by the FE without re-uploading.
//
// Inputs come straight from a CSV the user uploads; we don't try to fix
// them — a failed reconciliation is shown verbatim so the user can spot
// which line is off.
func (s *Service) ProposeOpeningBalances(ctx context.Context, userID, sessionID string,
	cc erpclient.CallContext, lines []OpeningBalanceLine, postingDate string,
) (*OpeningBalanceProposal, error) {
	st, err := s.LoadState(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, errors.New("trial balance is empty")
	}

	// Resolve account_number → account_id via the COA list. Account numbers
	// are unique per company so a single GET suffices.
	accounts := s.listOrEmpty(ctx, cc, "/accounting/accounts")
	byNumber := make(map[string]map[string]any, len(accounts))
	for _, a := range accounts {
		if n, ok := a["account_number"].(string); ok && n != "" {
			byNumber[n] = a
		}
	}

	prop, inventoryLine, inventoryAcct, err := buildProposal(lines, byNumber, postingDate)
	if err != nil {
		return nil, err
	}

	// Stock reconciliation: only when both an Inventory line exists in the
	// trial balance AND we can read a stock_ledger total via the API. The
	// stock_ledger endpoint may not exist for every install — failures
	// downgrade to a nil StockCheck rather than blocking submit.
	if inventoryLine != nil {
		if check := s.stockReconciliation(ctx, cc, inventoryAcct, *inventoryLine); check != nil {
			prop.StockCheck = check
		}
	}

	st.StepData[StepOpeningBalances] = map[string]any{
		"proposal":   prop,
		"submitted":  false,
		"je_id":      "",
	}
	if _, err := s.saveAndReturn(ctx, sessionID, st); err != nil {
		return nil, err
	}
	return prop, nil
}

// SubmitOpeningBalances turns the accepted proposal into a single Journal
// Entry and submits it via the public API. This is the only Tier-2
// auto-submit flow permitted in v1 (spec §4 Step 4): opening-balance JEs
// are by construction a one-time setup action and the human just reviewed
// the reconciliation proof one screen up.
func (s *Service) SubmitOpeningBalances(ctx context.Context, userID, sessionID string,
	cc erpclient.CallContext,
) (string, error) {
	st, err := s.LoadState(ctx, userID, sessionID)
	if err != nil {
		return "", err
	}
	stepData, ok := st.StepData[StepOpeningBalances].(map[string]any)
	if !ok {
		return "", errors.New("propose opening balances first")
	}
	if done, _ := stepData["submitted"].(bool); done {
		// Idempotent — return the existing JE id.
		if id, _ := stepData["je_id"].(string); id != "" {
			return id, nil
		}
	}
	propBlob, _ := stepData["proposal"]
	prop, err := decodeProposal(propBlob)
	if err != nil {
		return "", err
	}
	if !prop.Balanced {
		return "", fmt.Errorf("reconciliation failed: debit=%s credit=%s imbalance=%s",
			prop.TotalDebit, prop.TotalCredit, prop.Imbalance)
	}
	if prop.StockCheck != nil && !prop.StockCheck.Matches {
		return "", fmt.Errorf("stock reconciliation failed: trial balance %s vs stock ledger %s (diff %s)",
			prop.StockCheck.TrialBalanceDebit, prop.StockCheck.StockLedgerTotal, prop.StockCheck.Difference)
	}
	if len(prop.UnmappedAccounts) > 0 {
		return "", fmt.Errorf("%d account(s) not in COA: %s", len(prop.UnmappedAccounts),
			strings.Join(prop.UnmappedAccounts, ", "))
	}

	// Build the JE payload. The accounting/journal-entries endpoint expects
	// `accounts` as an array of {account_id, debit, credit} objects.
	jeLines := make([]map[string]any, 0, len(prop.Lines))
	for _, l := range prop.Lines {
		if l.ResolvedAccountID == "" {
			return "", fmt.Errorf("account %s missing resolved id", l.AccountNumber)
		}
		jeLines = append(jeLines, map[string]any{
			"account_id": l.ResolvedAccountID,
			"debit":      emptyAsZero(l.Debit),
			"credit":     emptyAsZero(l.Credit),
		})
	}

	var draft map[string]any
	if err := s.erp.Do(ctx, cc, "POST", "/accounting/journal-entries", map[string]any{
		"posting_date": prop.PostingDate,
		"user_remark":  "Opening balances (Migration Wizard Step 4)",
		"accounts":     jeLines,
	}, &draft); err != nil {
		return "", fmt.Errorf("create JE draft: %w", err)
	}
	jeID, _ := draft["id"].(string)
	if jeID == "" {
		return "", errors.New("JE create returned no id")
	}

	// Submit the JE. This is the Tier-2 auto-submit — explicit + audited.
	var submitted map[string]any
	if err := s.erp.Do(ctx, cc, "POST", "/accounting/journal-entries/"+jeID+"/submit", nil, &submitted); err != nil {
		return jeID, fmt.Errorf("submit JE: %w", err)
	}

	stepData["submitted"] = true
	stepData["je_id"] = jeID
	st.StepData[StepOpeningBalances] = stepData
	st.completeStep(StepOpeningBalances)
	st.CurrentStep = StepReadiness
	if _, err := s.saveAndReturn(ctx, sessionID, st); err != nil {
		return jeID, err
	}
	return jeID, nil
}

// stockReconciliation tries to fetch the stock_ledger total via the public
// API and compare it to the trial-balance debit for the Inventory account.
// Returns nil if we can't read the stock ledger or the trial balance has no
// inventory line. Failures here intentionally downgrade rather than blocking
// the wizard — Logica installs that don't use the Stock module shouldn't
// have to map this proof.
func (s *Service) stockReconciliation(ctx context.Context, cc erpclient.CallContext,
	inventoryAcct map[string]any, inventoryLine OpeningBalanceLine,
) *StockReconciliation {
	// Best-effort: fetch /stock/ledger?summary=1. If that endpoint isn't
	// available, return nil — see comment above for why this isn't fatal.
	var summary map[string]any
	if err := s.erp.Do(ctx, cc, "GET", "/stock/ledger?summary=1", nil, &summary); err != nil {
		return nil
	}
	rawTotal, _ := summary["total_valuation"].(string)
	if rawTotal == "" {
		return nil
	}
	ledgerTotal, err := decimal.NewFromString(rawTotal)
	if err != nil {
		return nil
	}
	tbDebit, _ := decimal.NewFromString(emptyAsZero(inventoryLine.Debit))
	diff := tbDebit.Sub(ledgerTotal).Abs()
	acctID, _ := inventoryAcct["id"].(string)
	acctNo, _ := inventoryAcct["account_number"].(string)
	r := &StockReconciliation{
		InventoryAccountID: acctID,
		InventoryAccountNo: acctNo,
		TrialBalanceDebit:  tbDebit.String(),
		StockLedgerTotal:   ledgerTotal.String(),
		Matches:            diff.IsZero(),
	}
	if !r.Matches {
		r.Difference = diff.String()
	}
	return r
}

// ---- helpers ----

func emptyAsZero(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0"
	}
	return s
}

func defaultPostingDate(in string) string {
	if in != "" {
		return in
	}
	// Same default the wizard uses for everything else — today, ISO date.
	return "" // empty signals "let the API default to today"
}

// buildProposal is the pure-validation core of ProposeOpeningBalances —
// no DB, no HTTP, no auth. Takes the user-supplied trial-balance lines
// and the COA lookup (account_number → row), returns the structured
// proposal plus the Inventory line + account (when an account_type=stock
// row matched) so the caller can layer the stock reconciliation on top.
//
// Surfaced as a standalone function so the reconciliation logic is
// unit-testable without spinning up a database. Mirrors spec §4 Step 4:
// debit + credit must be non-negative; never both on one line; account
// numbers must be in the COA; total debits must equal total credits.
func buildProposal(
	lines []OpeningBalanceLine,
	byNumber map[string]map[string]any,
	postingDate string,
) (*OpeningBalanceProposal, *OpeningBalanceLine, map[string]any, error) {
	var totalDebit, totalCredit decimal.Decimal
	var unmapped []string
	out := make([]OpeningBalanceLine, 0, len(lines))
	var inventoryLine *OpeningBalanceLine
	var inventoryAcct map[string]any

	for _, l := range lines {
		l.AccountNumber = strings.TrimSpace(l.AccountNumber)
		d, err := decimal.NewFromString(emptyAsZero(l.Debit))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("account %s debit: %w", l.AccountNumber, err)
		}
		c, err := decimal.NewFromString(emptyAsZero(l.Credit))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("account %s credit: %w", l.AccountNumber, err)
		}
		if d.Sign() < 0 || c.Sign() < 0 {
			return nil, nil, nil, fmt.Errorf("account %s: debit and credit must be non-negative", l.AccountNumber)
		}
		if !d.IsZero() && !c.IsZero() {
			return nil, nil, nil, fmt.Errorf("account %s: cannot have both a debit and a credit on the same opening line", l.AccountNumber)
		}
		l.Debit = d.String()
		l.Credit = c.String()
		totalDebit = totalDebit.Add(d)
		totalCredit = totalCredit.Add(c)

		acct, ok := byNumber[l.AccountNumber]
		if !ok {
			unmapped = append(unmapped, l.AccountNumber)
		} else {
			l.ResolvedAccountID, _ = acct["id"].(string)
			l.ResolvedAccountName, _ = acct["name"].(string)
			l.AccountType, _ = acct["account_type"].(string)
			if l.AccountType == "stock" {
				lc := l
				inventoryLine = &lc
				inventoryAcct = acct
			}
		}
		out = append(out, l)
	}

	prop := &OpeningBalanceProposal{
		Lines:            out,
		TotalDebit:       totalDebit.String(),
		TotalCredit:      totalCredit.String(),
		Balanced:         totalDebit.Equal(totalCredit),
		UnmappedAccounts: unmapped,
		PostingDate:      defaultPostingDate(postingDate),
	}
	if !prop.Balanced {
		diff := totalDebit.Sub(totalCredit).Abs()
		prop.Imbalance = diff.String()
	}
	return prop, inventoryLine, inventoryAcct, nil
}

// decodeProposal round-trips a stored proposal blob (from agent_session.state)
// back into the typed struct.
func decodeProposal(in any) (*OpeningBalanceProposal, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var p OpeningBalanceProposal
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
