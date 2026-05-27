// Package assetlocation manages a hierarchical master of physical sites
// where fixed assets sit. Tree is parent_id-based with cycle prevention on
// create + update; v1 doesn't compute lft/rgt because the trees are small
// (10-100 nodes for typical SMEs).
//
// Locations are referenced by asset.current_location_id and
// asset_movement.{from,to}_location_id. The pre-existing free-text fields
// stay for back-compat — the movement service mirrors location.name into
// them on submit.
package assetlocation

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
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "asset_location"

type AssetLocation struct {
	ID         string           `json:"id"`
	CompanyID  string           `json:"company_id"`
	ParentID   string           `json:"parent_id,omitempty"`
	Name       string           `json:"name"`
	Address    string           `json:"address,omitempty"`
	Latitude   *decimal.Decimal `json:"latitude,omitempty"`
	Longitude  *decimal.Decimal `json:"longitude,omitempty"`
	IsGroup    bool             `json:"is_group"`
	IsDeleted  bool             `json:"is_deleted"`
	CreatedAt  time.Time        `json:"created_at"`
}

type AssetLocationInput struct {
	ParentID  string `json:"parent_id,omitempty" doc:"id of parent location for tree placement"`
	Name      string `json:"name"`
	Address   string `json:"address,omitempty"`
	Latitude  string `json:"latitude,omitempty"  doc:"decimal degrees, -90..90; leave blank to clear"`
	Longitude string `json:"longitude,omitempty" doc:"decimal degrees, -180..180; leave blank to clear"`
	IsGroup   bool   `json:"is_group,omitempty"  doc:"group nodes can't be the leaf where an asset sits"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CRUD ----

func (s *Service) Create(ctx context.Context, companyID string, in AssetLocationInput) (*AssetLocation, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_location: unauthenticated")
	}
	if companyID == "" {
		return nil, errors.New("asset_location.company_id: required")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("asset_location.name: required")
	}
	id := dbx.NewIDWithPrefix("aloc")
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if in.ParentID != "" {
			// Parent must exist + be in the same company.
			var pCo string
			var pIsGroup bool
			if err := tx.QueryRow(ctx,
				`SELECT company_id, is_group FROM asset_location WHERE id = $1 AND is_deleted = false`,
				in.ParentID).Scan(&pCo, &pIsGroup); err != nil {
				return fmt.Errorf("parent_id: %w", err)
			}
			if pCo != companyID {
				return errors.New("parent_id: must be in the same company")
			}
			if !pIsGroup {
				return errors.New("parent_id: only group locations can have children")
			}
		}
		lat, err := parseCoord(in.Latitude, -90, 90, "latitude")
		if err != nil {
			return err
		}
		lng, err := parseCoord(in.Longitude, -180, 180, "longitude")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO asset_location (id, company_id, parent_id, name, address, latitude, longitude, is_group)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, companyID, nullable(in.ParentID), in.Name, nullable(in.Address), lat, lng, in.IsGroup); err != nil {
			if dbx.IsUniqueViolation(err) {
				return fmt.Errorf("asset_location: name %q already used in this company", in.Name)
			}
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, id string, in AssetLocationInput) (*AssetLocation, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_location: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("asset_location.name: required")
	}
	var out AssetLocation
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Cycle prevention: a node can't be its own ancestor. Walk up from
		// the proposed parent and bail if we ever land on `id`.
		if in.ParentID != "" {
			if in.ParentID == id {
				return errors.New("parent_id: cannot be self")
			}
			cur := in.ParentID
			seen := map[string]bool{id: true}
			for cur != "" {
				if seen[cur] {
					return errors.New("parent_id: would create a cycle")
				}
				seen[cur] = true
				var next *string
				if err := tx.QueryRow(ctx,
					`SELECT parent_id FROM asset_location WHERE id = $1`, cur).Scan(&next); err != nil {
					return err
				}
				if next == nil {
					break
				}
				cur = *next
			}
		}
		lat, err := parseCoord(in.Latitude, -90, 90, "latitude")
		if err != nil {
			return err
		}
		lng, err := parseCoord(in.Longitude, -180, 180, "longitude")
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE asset_location SET
			  parent_id = $1, name = $2, address = $3,
			  latitude = $4, longitude = $5,
			  is_group = $6
			WHERE id = $7 AND is_deleted = false`,
			nullable(in.ParentID), in.Name, nullable(in.Address), lat, lng, in.IsGroup, id)
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return fmt.Errorf("asset_location: name %q already used in this company", in.Name)
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("asset_location %s not found", id)
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

func (s *Service) Get(ctx context.Context, id string) (*AssetLocation, error) {
	var out *AssetLocation
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		l, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = l
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]AssetLocation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, coalesce(parent_id,''), name, coalesce(address,''),
		       latitude, longitude,
		       is_group, is_deleted, created_at
		FROM asset_location
		WHERE company_id = $1 AND is_deleted = false
		ORDER BY name`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetLocation
	for rows.Next() {
		var l AssetLocation
		if err := rows.Scan(&l.ID, &l.CompanyID, &l.ParentID, &l.Name, &l.Address,
			&l.Latitude, &l.Longitude,
			&l.IsGroup, &l.IsDeleted, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Delete soft-deletes. Refuses if any child or any active asset/movement
// still references this location.
func (s *Service) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("asset_location: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		var kids int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM asset_location WHERE parent_id = $1 AND is_deleted = false`, id).Scan(&kids); err != nil {
			return err
		}
		if kids > 0 {
			return fmt.Errorf("asset_location: %d child location(s) still attached", kids)
		}
		var refs int
		if err := tx.QueryRow(ctx, `
			SELECT
			  (SELECT count(*) FROM asset WHERE current_location_id = $1 AND docstatus <> 2)
			+ (SELECT count(*) FROM asset_movement
			   WHERE (from_location_id = $1 OR to_location_id = $1) AND docstatus <> 2)
			`, id).Scan(&refs); err != nil {
			return err
		}
		if refs > 0 {
			return fmt.Errorf("asset_location: %d active asset(s)/movement(s) reference this location", refs)
		}
		tag, err := tx.Exec(ctx,
			`UPDATE asset_location SET is_deleted = true WHERE id = $1 AND is_deleted = false`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("asset_location %s not found", id)
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

// ---- helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*AssetLocation, error) {
	var l AssetLocation
	var parent, addr *string
	err := tx.QueryRow(ctx, `
		SELECT id, company_id, parent_id, name, address, latitude, longitude,
		       is_group, is_deleted, created_at
		FROM asset_location WHERE id = $1`, id).
		Scan(&l.ID, &l.CompanyID, &parent, &l.Name, &addr, &l.Latitude, &l.Longitude,
			&l.IsGroup, &l.IsDeleted, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("asset_location %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if parent != nil {
		l.ParentID = *parent
	}
	if addr != nil {
		l.Address = *addr
	}
	return &l, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// parseCoord turns the API's string-form lat/lng into a decimal pointer
// or nil. Empty string = "clear the column". Validates the bounds at the
// service layer so the error surfaces clearly (the DB CHECK is a backstop).
func parseCoord(s string, lo, hi float64, field string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	f, _ := d.Float64()
	if f < lo || f > hi {
		return nil, fmt.Errorf("%s: must be in [%g, %g]", field, lo, hi)
	}
	return d, nil
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-asset-locations", Method: http.MethodGet,
		Path: "/assets/asset-locations", Summary: "List asset locations for the active company",
		Tags: []string{"Assets / Location"},
	}, func(ctx context.Context, _ *struct{}) (*alListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		ls, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &alListOut{Body: alListBody{Items: ls}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-asset-location", Method: http.MethodPost,
		Path: "/assets/asset-locations", Summary: "Create an asset location",
		Tags: []string{"Assets / Location"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *alCreateIn) (*alOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		l, err := h.Service.Create(ctx, co, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &alOut{Body: *l}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-asset-location", Method: http.MethodGet,
		Path: "/assets/asset-locations/{id}", Summary: "Get an asset location",
		Tags: []string{"Assets / Location"},
	}, func(ctx context.Context, in *alGetIn) (*alOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		l, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &alOut{Body: *l}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-asset-location", Method: http.MethodPut,
		Path: "/assets/asset-locations/{id}", Summary: "Update an asset location",
		Tags: []string{"Assets / Location"},
	}, func(ctx context.Context, in *alUpdateIn) (*alOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		l, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &alOut{Body: *l}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-asset-location", Method: http.MethodDelete,
		Path: "/assets/asset-locations/{id}", Summary: "Soft-delete an asset location",
		Tags: []string{"Assets / Location"},
	}, func(ctx context.Context, in *alGetIn) (*struct{ Body map[string]string }, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
}

type (
	alCreateIn struct{ Body AssetLocationInput }
	alUpdateIn struct {
		ID   string `path:"id"`
		Body AssetLocationInput
	}
	alGetIn struct {
		ID string `path:"id"`
	}
	alOut      struct{ Body AssetLocation }
	alListOut  struct{ Body alListBody }
	alListBody struct {
		Items []AssetLocation `json:"items"`
	}
)
