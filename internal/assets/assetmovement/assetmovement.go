// Package assetmovement records custodian / location handovers for fixed
// assets. No GL impact — purely a movement log + a denormalised mirror on
// asset.current_custodian / current_location so the asset detail page
// shows "currently with X at Y" without joining.
//
// Lifecycle: Draft → Submit (stamps asset.current_*) → optionally Cancel
// (rolls the asset back to its previous-movement state, or clears if this
// was the first).
package assetmovement

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const Doctype = "asset_movement"

// Movement types.
const (
	TypeIssue    = "issue"    // new asset → first custodian
	TypeReceipt  = "receipt"  // asset → store / not-in-use
	TypeTransfer = "transfer" // custodian/location change between two people/places
)

type AssetMovement struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	CompanyID      string             `json:"company_id"`
	AssetID        string             `json:"asset_id"`
	MovementDate   time.Time          `json:"movement_date"`
	MovementType   string             `json:"movement_type"`
	FromCustodian  string             `json:"from_custodian,omitempty"`
	ToCustodian    string             `json:"to_custodian"`
	FromLocation   string             `json:"from_location,omitempty"`
	ToLocation     string             `json:"to_location"`
	FromLocationID string             `json:"from_location_id,omitempty"`
	ToLocationID   string             `json:"to_location_id,omitempty"`
	Purpose        string             `json:"purpose,omitempty"`
	Remarks        string             `json:"remarks,omitempty"`
	Docstatus      submittable.Status `json:"docstatus"`
	SubmittedAt    *time.Time         `json:"submitted_at,omitempty"`
	CancelledAt    *time.Time         `json:"cancelled_at,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type MovementCreateInput struct {
	CompanyID      string         `json:"company_id,omitempty"`
	AssetID        string         `json:"asset_id"`
	MovementDate   string         `json:"movement_date"`
	MovementType   string         `json:"movement_type" doc:"issue | receipt | transfer"`
	FromCustodian  string         `json:"from_custodian,omitempty"`
	ToCustodian    string         `json:"to_custodian"`
	FromLocation   string         `json:"from_location,omitempty"   doc:"free-text; ignored when from_location_id is set"`
	ToLocation     string         `json:"to_location,omitempty"     doc:"free-text; ignored when to_location_id is set"`
	FromLocationID string         `json:"from_location_id,omitempty" doc:"FK to asset_location"`
	ToLocationID   string         `json:"to_location_id,omitempty"   doc:"FK to asset_location (preferred over to_location)"`
	Purpose        string         `json:"purpose,omitempty"`
	Remarks        string         `json:"remarks,omitempty"`
	CustomFields   map[string]any `json:"custom_fields,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CreateDraft ----

func (s *Service) CreateDraft(ctx context.Context, in MovementCreateInput) (*AssetMovement, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_movement: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("asset_movement.company_id: required")
	}
	if in.AssetID == "" {
		return nil, errors.New("asset_movement.asset_id: required")
	}
	if in.ToCustodian = strings.TrimSpace(in.ToCustodian); in.ToCustodian == "" {
		return nil, errors.New("asset_movement.to_custodian: required")
	}
	in.ToLocation = strings.TrimSpace(in.ToLocation)
	if in.ToLocation == "" && in.ToLocationID == "" {
		return nil, errors.New("asset_movement: to_location_id or to_location is required")
	}
	switch in.MovementType {
	case TypeIssue, TypeReceipt, TypeTransfer:
	default:
		return nil, fmt.Errorf("asset_movement.movement_type: must be issue | receipt | transfer (got %q)", in.MovementType)
	}
	md, err := time.Parse("2006-01-02", in.MovementDate)
	if err != nil {
		return nil, fmt.Errorf("asset_movement.movement_date: %w", err)
	}

	id := dbx.NewIDWithPrefix("amv")
	var out AssetMovement
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Asset must be submitted + not disposed.
		var assetCo, assetStatus string
		var docstatus int16
		if err := tx.QueryRow(ctx,
			`SELECT company_id, status, docstatus FROM asset WHERE id = $1`, in.AssetID).
			Scan(&assetCo, &assetStatus, &docstatus); err != nil {
			return fmt.Errorf("asset %s: %w", in.AssetID, err)
		}
		if assetCo != in.CompanyID {
			return errors.New("asset_movement: asset's company must match")
		}
		if docstatus != 1 {
			return errors.New("asset_movement: asset must be submitted")
		}
		switch assetStatus {
		case "Sold", "Scrapped", "Cancelled":
			return fmt.Errorf("asset_movement: cannot move a %s asset", assetStatus)
		}

		// Resolve location FKs → mirror name into the legacy text column.
		// Either form works; FK wins when both are supplied.
		if in.ToLocationID != "" {
			var lname, lco string
			var deleted bool
			if err := tx.QueryRow(ctx,
				`SELECT name, company_id, is_deleted FROM asset_location WHERE id = $1`,
				in.ToLocationID).Scan(&lname, &lco, &deleted); err != nil {
				return fmt.Errorf("to_location_id: %w", err)
			}
			if deleted {
				return errors.New("to_location_id: location is deleted")
			}
			if lco != in.CompanyID {
				return errors.New("to_location_id: must be in the same company as the asset")
			}
			in.ToLocation = lname
		}
		if in.FromLocationID != "" {
			var lname, lco string
			var deleted bool
			if err := tx.QueryRow(ctx,
				`SELECT name, company_id, is_deleted FROM asset_location WHERE id = $1`,
				in.FromLocationID).Scan(&lname, &lco, &deleted); err != nil {
				return fmt.Errorf("from_location_id: %w", err)
			}
			if deleted {
				return errors.New("from_location_id: location is deleted")
			}
			if lco != in.CompanyID {
				return errors.New("from_location_id: must be in the same company as the asset")
			}
			in.FromLocation = lname
		}

		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, md, nil)
		if err != nil {
			return err
		}
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO asset_movement (
				id, name, company_id, asset_id, movement_date, movement_type,
				from_custodian, to_custodian, from_location, to_location,
				from_location_id, to_location_id,
				purpose, remarks, custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)`,
			id, name, in.CompanyID, in.AssetID, md, in.MovementType,
			nullable(in.FromCustodian), in.ToCustodian,
			nullable(in.FromLocation), in.ToLocation,
			nullable(in.FromLocationID), nullable(in.ToLocationID),
			in.Purpose, nullable(in.Remarks), cf, p.UserID); err != nil {
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

// ---- Submit ----

// Submit stamps the asset's current_custodian + current_location with this
// movement's to_* values, so the asset detail page reflects "currently
// with…" without joining the movement table on every read.
func (s *Service) Submit(ctx context.Context, id string) (*AssetMovement, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_movement: unauthenticated")
	}
	var out AssetMovement
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		mv, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if mv.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if _, err := tx.Exec(ctx, `
			UPDATE asset_movement
			SET docstatus = 1, submitted_at = now(), submitted_by = $1, updated_by = $1
			WHERE id = $2`, p.UserID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE asset SET current_custodian = $1, current_location = $2,
			       current_location_id = $3, updated_by = $4
			WHERE id = $5`, mv.ToCustodian, mv.ToLocation,
			nullable(mv.ToLocationID), p.UserID, mv.AssetID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionSubmit, audit.Diff{}); err != nil {
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

// ---- Cancel ----

// Cancel reverses Submit's stamp: the asset's current_* fields rewind to
// the most recent OTHER submitted movement's to_* values (or NULL if none).
func (s *Service) Cancel(ctx context.Context, id string) (*AssetMovement, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_movement: unauthenticated")
	}
	var out AssetMovement
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		mv, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if mv.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if _, err := tx.Exec(ctx, `
			UPDATE asset_movement
			SET docstatus = 2, cancelled_at = now(), cancelled_by = $1, updated_by = $1
			WHERE id = $2`, p.UserID, id); err != nil {
			return err
		}
		// Roll the asset back to the most-recent OTHER submitted movement.
		var prevCustodian, prevLocation, prevLocationID *string
		_ = tx.QueryRow(ctx, `
			SELECT to_custodian, to_location, to_location_id FROM asset_movement
			WHERE asset_id = $1 AND id <> $2 AND docstatus = 1
			ORDER BY movement_date DESC, created_at DESC LIMIT 1`,
			mv.AssetID, id).Scan(&prevCustodian, &prevLocation, &prevLocationID)
		if _, err := tx.Exec(ctx, `
			UPDATE asset SET current_custodian = $1, current_location = $2,
			       current_location_id = $3, updated_by = $4
			WHERE id = $5`, prevCustodian, prevLocation, prevLocationID, p.UserID, mv.AssetID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionCancel, audit.Diff{}); err != nil {
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

// ---- Get / List ----

func (s *Service) Get(ctx context.Context, id string) (*AssetMovement, error) {
	var out *AssetMovement
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		mv, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = mv
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]AssetMovement, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM asset_movement WHERE company_id = $1
		ORDER BY movement_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]AssetMovement, 0, len(ids))
	for _, id := range ids {
		mv, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *mv)
	}
	return out, nil
}

// ---- helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*AssetMovement, error) {
	var (
		mv                                              AssetMovement
		submittedAt, cancelledAt                        *time.Time
		fromCustodian, fromLocation, remarks, purpose   *string
		fromLocationID, toLocationID                    *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, asset_id, movement_date, movement_type,
		       from_custodian, to_custodian, from_location, to_location,
		       from_location_id, to_location_id,
		       purpose, remarks, docstatus, submitted_at, cancelled_at, created_at, updated_at
		FROM asset_movement WHERE id = $1`, id).
		Scan(&mv.ID, &mv.Name, &mv.CompanyID, &mv.AssetID, &mv.MovementDate, &mv.MovementType,
			&fromCustodian, &mv.ToCustodian, &fromLocation, &mv.ToLocation,
			&fromLocationID, &toLocationID,
			&purpose, &remarks, &mv.Docstatus, &submittedAt, &cancelledAt, &mv.CreatedAt, &mv.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("asset_movement %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if fromCustodian != nil {
		mv.FromCustodian = *fromCustodian
	}
	if fromLocation != nil {
		mv.FromLocation = *fromLocation
	}
	if fromLocationID != nil {
		mv.FromLocationID = *fromLocationID
	}
	if toLocationID != nil {
		mv.ToLocationID = *toLocationID
	}
	if purpose != nil {
		mv.Purpose = *purpose
	}
	if remarks != nil {
		mv.Remarks = *remarks
	}
	mv.SubmittedAt = submittedAt
	mv.CancelledAt = cancelledAt
	return &mv, nil
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

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-asset-movements", Method: http.MethodGet,
		Path: "/assets/asset-movements", Summary: "List asset movements",
		Tags: []string{"Assets / Movement"},
	}, func(ctx context.Context, _ *struct{}) (*amListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		mvs, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &amListOut{Body: amListBody{Items: mvs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-asset-movement", Method: http.MethodPost,
		Path: "/assets/asset-movements", Summary: "Create an asset movement draft",
		Tags: []string{"Assets / Movement"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *amCreateIn) (*amOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		mv, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &amOut{Body: *mv}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-asset-movement", Method: http.MethodGet,
		Path: "/assets/asset-movements/{id}", Summary: "Get an asset movement",
		Tags: []string{"Assets / Movement"},
	}, func(ctx context.Context, in *amGetIn) (*amOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		mv, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &amOut{Body: *mv}, nil
	})
	for _, op := range []struct {
		id, path, summary string
		fn                func(context.Context, string) (*AssetMovement, error)
		action            permission.Action
	}{
		{"submit-asset-movement", "submit", "Submit a movement (stamps asset.current_*)", h.Service.Submit, permission.ActionSubmit},
		{"cancel-asset-movement", "cancel", "Cancel a movement (rolls asset.current_* back to the prior movement)", h.Service.Cancel, permission.ActionCancel},
	} {
		op := op
		huma.Register(api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost,
			Path: "/assets/asset-movements/{id}/" + op.path, Summary: op.summary,
			Tags: []string{"Assets / Movement"},
		}, func(ctx context.Context, in *amGetIn) (*amOut, error) {
			if err := h.Perm.Check(ctx, Doctype, op.action); err != nil {
				return nil, httpx.MapError(err)
			}
			mv, err := op.fn(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &amOut{Body: *mv}, nil
		})
	}
}

type (
	amCreateIn struct{ Body MovementCreateInput }
	amOut      struct{ Body AssetMovement }
	amListOut  struct{ Body amListBody }
	amListBody struct {
		Items []AssetMovement `json:"items"`
	}
	amGetIn struct {
		ID string `path:"id"`
	}
)
