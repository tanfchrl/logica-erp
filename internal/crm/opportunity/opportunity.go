// Package opportunity implements the CRM Opportunity (deal) doctype —
// the pipeline pivot. Twenty-style: kanban-first, seven stages, one
// amount, one owner. No quotation, no sales-order chain (those live in
// the order-to-cash flow).
//
// Key actions:
//
//   CreateDraft → opportunity at stage=prospecting
//   SetStage    → moves between stages (kanban drag-drop)
//   MarkLost    → sets stage=closed_lost + lost_reason + closed_at
//   MarkWon     → sets stage=closed_won + probability=100 + closed_at
//
// SetStage is idempotent — same-stage call is a no-op so the FE can
// optimistic-update without juggling.
package opportunity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "opportunity"

// Stage enum — kept in sync with the DB CHECK constraint.
const (
	StageProspecting   = "prospecting"
	StageQualification = "qualification"
	StageProposal      = "proposal"
	StageNegotiation   = "negotiation"
	StageClosedWon     = "closed_won"
	StageClosedLost    = "closed_lost"
)

const (
	FromLead     = "lead"
	FromCustomer = "customer"
)

// StageOrder is the canonical kanban column order. Exported so the FE can
// import + render without re-declaring.
var StageOrder = []string{
	StageProspecting, StageQualification, StageProposal, StageNegotiation,
	StageClosedWon, StageClosedLost,
}

// defaultProbability returns the suggested probability % for a stage. The
// Twenty default funnel — users can override per opportunity.
func defaultProbability(stage string) decimal.Decimal {
	switch stage {
	case StageProspecting:
		return decimal.NewFromInt(10)
	case StageQualification:
		return decimal.NewFromInt(25)
	case StageProposal:
		return decimal.NewFromInt(50)
	case StageNegotiation:
		return decimal.NewFromInt(75)
	case StageClosedWon:
		return decimal.NewFromInt(100)
	case StageClosedLost:
		return decimal.Zero
	}
	return decimal.Zero
}

type Opportunity struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	CompanyID           string          `json:"company_id"`
	Subject             string          `json:"subject"`
	OpportunityFrom     string          `json:"opportunity_from"`
	PartyID             string          `json:"party_id"`
	PartyName           string          `json:"party_name,omitempty"`
	Stage               string          `json:"stage"`
	ProbabilityPct      decimal.Decimal `json:"probability_pct"`
	Amount              decimal.Decimal `json:"amount"`
	Currency            string          `json:"currency"`
	ExpectedCloseDate   *time.Time      `json:"expected_close_date,omitempty"`
	OwnerUserID         string          `json:"owner_user_id,omitempty"`
	Source              string          `json:"source,omitempty"`
	LostReason          string          `json:"lost_reason,omitempty"`
	ClosedAt            *time.Time      `json:"closed_at,omitempty"`
	Remarks             string          `json:"remarks,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type OpportunityCreateInput struct {
	CompanyID         string         `json:"company_id,omitempty"`
	Subject           string         `json:"subject"`
	OpportunityFrom   string         `json:"opportunity_from" doc:"lead | customer"`
	PartyID           string         `json:"party_id"`
	Stage             string         `json:"stage,omitempty" doc:"defaults to prospecting"`
	ProbabilityPct    string         `json:"probability_pct,omitempty"`
	Amount            string         `json:"amount,omitempty"`
	Currency          string         `json:"currency,omitempty"`
	ExpectedCloseDate string         `json:"expected_close_date,omitempty"`
	OwnerUserID       string         `json:"owner_user_id,omitempty"`
	Source            string         `json:"source,omitempty"`
	Remarks           string         `json:"remarks,omitempty"`
	CustomFields      map[string]any `json:"custom_fields,omitempty"`
}

type OpportunityUpdateInput struct {
	Subject           string `json:"subject"`
	Amount            string `json:"amount,omitempty"`
	Currency          string `json:"currency,omitempty"`
	ExpectedCloseDate string `json:"expected_close_date,omitempty"`
	OwnerUserID       string `json:"owner_user_id,omitempty"`
	Source            string `json:"source,omitempty"`
	Remarks           string `json:"remarks,omitempty"`
	ProbabilityPct    string `json:"probability_pct,omitempty"`
}

type SetStageInput struct {
	Stage string `json:"stage"`
}

type MarkLostInput struct {
	LostReason string `json:"lost_reason"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CreateDraft ----

func (s *Service) CreateDraft(ctx context.Context, in OpportunityCreateInput) (*Opportunity, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("opportunity: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("opportunity.company_id: required")
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, errors.New("opportunity.subject: required")
	}
	switch in.OpportunityFrom {
	case FromLead, FromCustomer:
	default:
		return nil, fmt.Errorf("opportunity.opportunity_from: must be lead | customer (got %q)", in.OpportunityFrom)
	}
	if strings.TrimSpace(in.PartyID) == "" {
		return nil, errors.New("opportunity.party_id: required")
	}
	stage := in.Stage
	if stage == "" {
		stage = StageProspecting
	}
	if !validStage(stage) {
		return nil, fmt.Errorf("opportunity.stage: invalid %q", stage)
	}

	prob := defaultProbability(stage)
	if in.ProbabilityPct != "" {
		v, err := decimal.NewFromString(in.ProbabilityPct)
		if err != nil {
			return nil, fmt.Errorf("opportunity.probability_pct: %w", err)
		}
		prob = v
	}

	amount := decimal.Zero
	if in.Amount != "" {
		v, err := decimal.NewFromString(in.Amount)
		if err != nil {
			return nil, fmt.Errorf("opportunity.amount: %w", err)
		}
		amount = v
	}
	currency := in.Currency
	if currency == "" {
		currency = "IDR"
	}

	var expectedClose *time.Time
	if in.ExpectedCloseDate != "" {
		t, err := time.Parse("2006-01-02", in.ExpectedCloseDate)
		if err != nil {
			return nil, fmt.Errorf("opportunity.expected_close_date: %w", err)
		}
		expectedClose = &t
	}

	owner := in.OwnerUserID
	if owner == "" {
		owner = p.UserID
	}

	id := dbx.NewIDWithPrefix("opp")
	var out Opportunity
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Resolve party_name from the underlying party for display
		// convenience. Best-effort — empty parent record won't block.
		partyName := resolvePartyName(ctx, tx, in.OpportunityFrom, in.PartyID)

		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		name, err := naming.Next(ctx, tx, seriesID, pattern, now, nil)
		if err != nil {
			return err
		}
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO opportunity (
				id, name, company_id, subject,
				opportunity_from, party_id, party_name,
				stage, probability_pct, amount, currency,
				expected_close_date, owner_user_id, source, remarks,
				custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)`,
			id, name, in.CompanyID, in.Subject,
			in.OpportunityFrom, in.PartyID, nullable(partyName),
			stage, prob, amount, currency,
			nullableTime(expectedClose), nullable(owner), nullable(in.Source), nullable(in.Remarks),
			cf, p.UserID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionCreate, audit.Diff{After: in}); err != nil {
			return err
		}
		loaded, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

// ---- Update (non-stage edits) ----

func (s *Service) Update(ctx context.Context, id string, in OpportunityUpdateInput) (*Opportunity, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("opportunity: unauthenticated")
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, errors.New("opportunity.subject: required")
	}
	var amount, prob decimal.Decimal
	if in.Amount != "" {
		v, err := decimal.NewFromString(in.Amount)
		if err != nil {
			return nil, fmt.Errorf("opportunity.amount: %w", err)
		}
		amount = v
	}
	if in.ProbabilityPct != "" {
		v, err := decimal.NewFromString(in.ProbabilityPct)
		if err != nil {
			return nil, fmt.Errorf("opportunity.probability_pct: %w", err)
		}
		prob = v
	}
	var expectedClose *time.Time
	if in.ExpectedCloseDate != "" {
		t, err := time.Parse("2006-01-02", in.ExpectedCloseDate)
		if err != nil {
			return nil, fmt.Errorf("opportunity.expected_close_date: %w", err)
		}
		expectedClose = &t
	}
	currency := in.Currency
	if currency == "" {
		currency = "IDR"
	}

	var out Opportunity
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		// If the user didn't provide probability, keep the existing value.
		newProb := prob
		if in.ProbabilityPct == "" {
			newProb = existing.ProbabilityPct
		}
		newAmount := amount
		if in.Amount == "" {
			newAmount = existing.Amount
		}
		tag, err := tx.Exec(ctx, `
			UPDATE opportunity SET
			  subject = $1, amount = $2, currency = $3,
			  expected_close_date = $4, owner_user_id = $5,
			  source = $6, remarks = $7, probability_pct = $8,
			  updated_by = $9
			WHERE id = $10`,
			in.Subject, newAmount, currency,
			nullableTime(expectedClose), nullable(in.OwnerUserID),
			nullable(in.Source), nullable(in.Remarks), newProb,
			p.UserID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("opportunity %s not found", id)
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in}); err != nil {
			return err
		}
		loaded, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

// ---- SetStage (kanban drag-drop) ----

// SetStage moves the opportunity to a new stage. Idempotent — same-stage
// call is a silent no-op so the FE can optimistic-update without juggling.
// closed_lost requires going through MarkLost (so lost_reason isn't bypassed).
func (s *Service) SetStage(ctx context.Context, id string, stage string) (*Opportunity, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("opportunity: unauthenticated")
	}
	if !validStage(stage) {
		return nil, fmt.Errorf("opportunity.stage: invalid %q", stage)
	}
	if stage == StageClosedLost {
		return nil, errors.New("opportunity: use POST /mark-lost to move to closed_lost — it requires a lost_reason")
	}

	var out Opportunity
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if existing.Stage == stage {
			out = *existing
			return nil
		}

		// closed_won bumps probability to 100 and stamps closed_at.
		newProb := existing.ProbabilityPct
		var closedAt *time.Time
		if stage == StageClosedWon {
			newProb = decimal.NewFromInt(100)
			now := time.Now().UTC()
			closedAt = &now
		} else {
			// Reopening from closed_* clears closed_at.
			if existing.Stage == StageClosedWon || existing.Stage == StageClosedLost {
				closedAt = nil
				newProb = defaultProbability(stage)
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE opportunity SET
			  stage = $1, probability_pct = $2,
			  closed_at = $3, lost_reason = NULL,
			  updated_by = $4
			WHERE id = $5`,
			stage, newProb, nullableTime(closedAt), p.UserID, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, "set_stage",
			audit.Diff{Before: map[string]any{"stage": existing.Stage}, After: map[string]any{"stage": stage}}); err != nil {
			return err
		}
		loaded, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

// ---- MarkLost ----

func (s *Service) MarkLost(ctx context.Context, id string, in MarkLostInput) (*Opportunity, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("opportunity: unauthenticated")
	}
	in.LostReason = strings.TrimSpace(in.LostReason)
	if in.LostReason == "" {
		return nil, errors.New("opportunity.lost_reason: required when marking lost")
	}
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE opportunity SET
			  stage = $1, probability_pct = 0,
			  closed_at = now(), lost_reason = $2,
			  updated_by = $3
			WHERE id = $4`,
			StageClosedLost, in.LostReason, p.UserID, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, "mark_lost",
			audit.Diff{Before: map[string]any{"stage": existing.Stage}, After: map[string]any{"stage": StageClosedLost, "lost_reason": in.LostReason}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// ---- Get / List ----

func (s *Service) Get(ctx context.Context, id string) (*Opportunity, error) {
	var out *Opportunity
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		o, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = o
		return nil
	})
	return out, err
}

// List returns opportunities in the active company. Optional `stage` query
// narrows to a single kanban column. Default 500-row cap.
func (s *Service) List(ctx context.Context, companyID, stage string) ([]Opportunity, error) {
	args := []any{companyID}
	q := `SELECT id FROM opportunity WHERE company_id = $1`
	if stage != "" {
		q += ` AND stage = $2`
		args = append(args, stage)
	}
	q += ` ORDER BY updated_at DESC LIMIT 500`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	out := make([]Opportunity, 0, len(ids))
	for _, id := range ids {
		o, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, nil
}

// ---- helpers ----

func validStage(s string) bool {
	for _, v := range StageOrder {
		if v == s {
			return true
		}
	}
	return false
}

func resolvePartyName(ctx context.Context, tx pgx.Tx, kind, id string) string {
	var name string
	switch kind {
	case FromLead:
		_ = tx.QueryRow(ctx, `SELECT lead_name FROM lead WHERE id = $1`, id).Scan(&name)
	case FromCustomer:
		_ = tx.QueryRow(ctx, `SELECT display_name FROM customer WHERE id = $1`, id).Scan(&name)
	}
	return name
}

func load(ctx context.Context, tx pgx.Tx, id string) (*Opportunity, error) {
	var (
		o                                                    Opportunity
		partyName, owner, source, lostReason, remarks        *string
		expectedClose, closedAt                              *time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, subject,
		       opportunity_from, party_id, party_name,
		       stage, probability_pct, amount, currency,
		       expected_close_date, owner_user_id, source,
		       lost_reason, closed_at, remarks,
		       created_at, updated_at
		FROM opportunity WHERE id = $1`, id).
		Scan(&o.ID, &o.Name, &o.CompanyID, &o.Subject,
			&o.OpportunityFrom, &o.PartyID, &partyName,
			&o.Stage, &o.ProbabilityPct, &o.Amount, &o.Currency,
			&expectedClose, &owner, &source,
			&lostReason, &closedAt, &remarks,
			&o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("opportunity %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if partyName != nil {
		o.PartyName = *partyName
	}
	if owner != nil {
		o.OwnerUserID = *owner
	}
	if source != nil {
		o.Source = *source
	}
	if lostReason != nil {
		o.LostReason = *lostReason
	}
	if remarks != nil {
		o.Remarks = *remarks
	}
	o.ExpectedCloseDate = expectedClose
	o.ClosedAt = closedAt
	return &o, nil
}

func pickSeries(ctx context.Context, tx pgx.Tx, doctype, companyID string) (string, string, error) {
	var id, pat string
	err := tx.QueryRow(ctx, `
		SELECT id, pattern FROM naming_series
		WHERE doctype = $1 AND is_default = true AND (company_id = $2 OR company_id IS NULL)
		ORDER BY company_id NULLS LAST LIMIT 1`, doctype, companyID).Scan(&id, &pat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("no default naming series for %s", doctype)
	}
	return id, pat, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-opportunities", Method: http.MethodGet,
		Path: "/crm/opportunities", Summary: "List opportunities (optionally narrowed to one stage)",
		Tags: []string{"CRM / Opportunity"},
	}, func(ctx context.Context, in *oppListIn) (*oppListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		os, err := h.Service.List(ctx, co, in.Stage)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &oppListOut{Body: oppListBody{Items: os, StageOrder: StageOrder}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-opportunity", Method: http.MethodPost,
		Path: "/crm/opportunities", Summary: "Create an opportunity (draft at prospecting stage)",
		Tags: []string{"CRM / Opportunity"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *oppCreateIn) (*oppOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		o, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &oppOut{Body: *o}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-opportunity", Method: http.MethodGet,
		Path: "/crm/opportunities/{id}", Summary: "Get an opportunity",
		Tags: []string{"CRM / Opportunity"},
	}, func(ctx context.Context, in *oppGetIn) (*oppOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		o, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &oppOut{Body: *o}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-opportunity", Method: http.MethodPut,
		Path: "/crm/opportunities/{id}", Summary: "Update an opportunity's non-stage fields",
		Tags: []string{"CRM / Opportunity"},
	}, func(ctx context.Context, in *oppUpdateIn) (*oppOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		o, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &oppOut{Body: *o}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "set-opportunity-stage", Method: http.MethodPost,
		Path: "/crm/opportunities/{id}/set-stage", Summary: "Move an opportunity to a new stage (kanban drag-drop)",
		Tags: []string{"CRM / Opportunity"},
	}, func(ctx context.Context, in *oppStageIn) (*oppOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		o, err := h.Service.SetStage(ctx, in.ID, in.Body.Stage)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &oppOut{Body: *o}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "mark-opportunity-lost", Method: http.MethodPost,
		Path: "/crm/opportunities/{id}/mark-lost", Summary: "Mark an opportunity as Closed Lost (lost_reason required)",
		Tags: []string{"CRM / Opportunity"},
	}, func(ctx context.Context, in *oppLostIn) (*oppOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		o, err := h.Service.MarkLost(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &oppOut{Body: *o}, nil
	})
}

type (
	oppCreateIn struct{ Body OpportunityCreateInput }
	oppUpdateIn struct {
		ID   string `path:"id"`
		Body OpportunityUpdateInput
	}
	oppGetIn struct {
		ID string `path:"id"`
	}
	oppListIn struct {
		Stage string `query:"stage" doc:"narrow to one stage"`
	}
	oppStageIn struct {
		ID   string `path:"id"`
		Body SetStageInput
	}
	oppLostIn struct {
		ID   string `path:"id"`
		Body MarkLostInput
	}
	oppOut     struct{ Body Opportunity }
	oppListOut struct{ Body oppListBody }
	oppListBody struct {
		Items      []Opportunity `json:"items"`
		StageOrder []string      `json:"stage_order"`
	}
)
