package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/money"
)

// ErrImbalanced is returned by PostGL when the sum of debits does not equal the sum of credits.
var ErrImbalanced = errors.New("ledger: voucher is not balanced")

// PostGL appends GL entries for a voucher, enforcing the invariant
// that sum(debit) == sum(credit) in base currency.
// Caller supplies the open transaction. Returns the inserted entry IDs in input order.
func PostGL(ctx context.Context, tx pgx.Tx, v Voucher, entries []Entry) ([]string, error) {
	if len(entries) == 0 {
		return nil, errors.New("ledger: no entries to post")
	}

	debits := make([]decimal.Decimal, 0, len(entries))
	credits := make([]decimal.Decimal, 0, len(entries))
	for i, e := range entries {
		if err := money.Validate(e.Debit); err != nil {
			return nil, fmt.Errorf("entry[%d].debit: %w", i, err)
		}
		if err := money.Validate(e.Credit); err != nil {
			return nil, fmt.Errorf("entry[%d].credit: %w", i, err)
		}
		if e.Debit.IsZero() == e.Credit.IsZero() {
			return nil, fmt.Errorf("entry[%d]: exactly one of debit/credit must be > 0", i)
		}
		debits = append(debits, e.Debit)
		credits = append(credits, e.Credit)
	}
	if err := money.SumBalanced(debits, credits); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrImbalanced, err.Error())
	}

	ids := make([]string, len(entries))
	batch := &pgx.Batch{}
	for i, e := range entries {
		id := dbx.NewIDWithPrefix("gle")
		ids[i] = id
		debitAC := e.DebitInAccountCurrency
		creditAC := e.CreditInAccountCurrency
		if debitAC.IsZero() && !e.Debit.IsZero() {
			debitAC = e.Debit
		}
		if creditAC.IsZero() && !e.Credit.IsZero() {
			creditAC = e.Credit
		}
		batch.Queue(`
			INSERT INTO gl_entry (
				id, company_id, posting_date, account_id,
				party_type, party_id, cost_center_id, project_id,
				debit, credit, account_currency,
				debit_in_account_currency, credit_in_account_currency,
				against, voucher_type, voucher_id, voucher_name, remarks,
				fiscal_year, created_by
			) VALUES (
				$1,$2,$3,$4,
				$5,$6,$7,$8,
				$9,$10,$11,
				$12,$13,
				$14,$15,$16,$17,$18,
				$19,$20
			)`,
			id, v.CompanyID, v.PostingDate, e.AccountID,
			nullString(string(e.PartyType)), nullString(e.PartyID), nullString(e.CostCenterID), nullString(e.ProjectID),
			e.Debit, e.Credit, e.AccountCurrency,
			debitAC, creditAC,
			nullString(e.Against), v.Type, v.ID, v.Name, nullString(e.Remarks),
			v.FiscalYearID, v.CreatedBy,
		)
	}
	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for range entries {
		if _, err := br.Exec(); err != nil {
			return nil, fmt.Errorf("ledger: insert gl_entry: %w", err)
		}
	}
	return ids, nil
}

// CancelGL marks all gl_entry rows for a voucher as cancelled and posts the inverse entries.
// Returns the inserted reversing entry IDs.
func CancelGL(ctx context.Context, tx pgx.Tx, voucherType, voucherID, cancelledByUserID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, company_id, posting_date, account_id, party_type, party_id, cost_center_id, project_id,
		       debit, credit, account_currency, debit_in_account_currency, credit_in_account_currency,
		       against, voucher_name, fiscal_year
		FROM gl_entry
		WHERE voucher_type = $1 AND voucher_id = $2 AND is_cancelled = false`, voucherType, voucherID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type original struct {
		id, companyID, accountID, accountCurrency, against, voucherName, fiscalYear string
		partyType, partyID, costCenter, projectID                                  *string
		postingDate                                                                 any
		debit, credit, debitAC, creditAC                                           decimal.Decimal
	}
	var originals []original
	for rows.Next() {
		var o original
		if err := rows.Scan(&o.id, &o.companyID, &o.postingDate, &o.accountID,
			&o.partyType, &o.partyID, &o.costCenter, &o.projectID,
			&o.debit, &o.credit, &o.accountCurrency, &o.debitAC, &o.creditAC,
			&o.against, &o.voucherName, &o.fiscalYear); err != nil {
			return nil, err
		}
		originals = append(originals, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(originals) == 0 {
		return nil, errors.New("ledger: nothing to cancel for voucher")
	}

	out := make([]string, 0, len(originals))
	for _, o := range originals {
		id := dbx.NewIDWithPrefix("gle")
		out = append(out, id)
		_, err := tx.Exec(ctx, `
			INSERT INTO gl_entry (
				id, company_id, posting_date, account_id,
				party_type, party_id, cost_center_id, project_id,
				debit, credit, account_currency,
				debit_in_account_currency, credit_in_account_currency,
				against, voucher_type, voucher_id, voucher_name, remarks,
				fiscal_year, created_by
			) VALUES (
				$1,$2,$3,$4,
				$5,$6,$7,$8,
				$9,$10,$11,
				$12,$13,
				$14,$15,$16,$17,$18,
				$19,$20
			)`,
			id, o.companyID, o.postingDate, o.accountID,
			o.partyType, o.partyID, o.costCenter, o.projectID,
			o.credit, o.debit, o.accountCurrency, // swapped
			o.creditAC, o.debitAC,
			o.against, voucherType, voucherID, o.voucherName, "Cancellation reversal",
			o.fiscalYear, cancelledByUserID,
		)
		if err != nil {
			return nil, fmt.Errorf("ledger: insert reversing gl_entry: %w", err)
		}
		// Link the original to its reversal for audit. We deliberately do NOT
		// set is_cancelled on the original — both the original and the reversal
		// remain visible in reports so they net to zero naturally. The
		// is_cancelled flag is reserved for entries that should be hidden
		// entirely (e.g. periodic-closing snapshots in Phase 6).
		if _, err := tx.Exec(ctx,
			`UPDATE gl_entry SET cancelled_by_entry_id = $1 WHERE id = $2`,
			id, o.id); err != nil {
			return nil, fmt.Errorf("ledger: link reversal: %w", err)
		}
	}
	return out, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
