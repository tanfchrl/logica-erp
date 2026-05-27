// Package connectors implements a generic config store for third-party
// integrations: payment gateways, bank feeds, marketplaces, shipping carriers.
//
// Each connector stores provider-specific credentials (opaque) plus
// non-secret config (account number, sender id, etc.). Actual API integration
// per-provider is a downstream concern; this package is the credential vault.
package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "connector_config"

// ProviderCatalog — what's pickable in the UI per kind. Add new providers
// here as integration work lands.
var ProviderCatalog = []ProviderDef{
	// Payment gateways — Indonesian-first
	{Kind: "payment_gateway", Provider: "midtrans", Label: "Midtrans (Snap + Core)",   Fields: []ConnectorFieldDef{{Key: "server_key", Label: "Server key", Secret: true}, {Key: "client_key", Label: "Client key"}}},
	{Kind: "payment_gateway", Provider: "xendit",   Label: "Xendit",                    Fields: []ConnectorFieldDef{{Key: "secret_key", Label: "Secret key", Secret: true}, {Key: "callback_token", Label: "Callback verification token", Secret: true}}},
	{Kind: "payment_gateway", Provider: "doku",     Label: "DOKU",                      Fields: []ConnectorFieldDef{{Key: "client_id", Label: "Client ID"}, {Key: "shared_key", Label: "Shared key", Secret: true}}},
	{Kind: "payment_gateway", Provider: "ipaymu",   Label: "iPaymu",                    Fields: []ConnectorFieldDef{{Key: "api_key", Label: "API key", Secret: true}, {Key: "va", Label: "Virtual account"}}},

	// Bank feeds
	{Kind: "bank_feed",       Provider: "bca",      Label: "BCA — manual OFX/CSV",      Fields: []ConnectorFieldDef{{Key: "account_number", Label: "Account number"}}},
	{Kind: "bank_feed",       Provider: "mandiri",  Label: "Mandiri — manual OFX/CSV",  Fields: []ConnectorFieldDef{{Key: "account_number", Label: "Account number"}}},
	{Kind: "bank_feed",       Provider: "bri",      Label: "BRI — manual OFX/CSV",      Fields: []ConnectorFieldDef{{Key: "account_number", Label: "Account number"}}},
	{Kind: "bank_feed",       Provider: "bni",      Label: "BNI — manual OFX/CSV",      Fields: []ConnectorFieldDef{{Key: "account_number", Label: "Account number"}}},
	{Kind: "bank_feed",       Provider: "brick",    Label: "Brick (direct connect)",    Fields: []ConnectorFieldDef{{Key: "public_token", Label: "Public token", Secret: true}}},
	{Kind: "bank_feed",       Provider: "finantier",Label: "Finantier (direct connect)",Fields: []ConnectorFieldDef{{Key: "api_key", Label: "API key", Secret: true}}},

	// Marketplaces
	{Kind: "marketplace",     Provider: "tokopedia",Label: "Tokopedia",                 Fields: []ConnectorFieldDef{{Key: "client_id", Label: "Client ID"}, {Key: "client_secret", Label: "Client secret", Secret: true}, {Key: "shop_id", Label: "Shop ID"}}},
	{Kind: "marketplace",     Provider: "shopee",   Label: "Shopee",                    Fields: []ConnectorFieldDef{{Key: "partner_id", Label: "Partner ID"}, {Key: "partner_key", Label: "Partner key", Secret: true}, {Key: "shop_id", Label: "Shop ID"}}},
	{Kind: "marketplace",     Provider: "tiktok",   Label: "TikTok Shop",               Fields: []ConnectorFieldDef{{Key: "app_key", Label: "App key"}, {Key: "app_secret", Label: "App secret", Secret: true}, {Key: "shop_id", Label: "Shop ID"}}},
	{Kind: "marketplace",     Provider: "lazada",   Label: "Lazada",                    Fields: []ConnectorFieldDef{{Key: "app_key", Label: "App key"}, {Key: "app_secret", Label: "App secret", Secret: true}}},

	// Shipping
	{Kind: "shipping",        Provider: "jne",      Label: "JNE",                       Fields: []ConnectorFieldDef{{Key: "username", Label: "Username"}, {Key: "api_key", Label: "API key", Secret: true}}},
	{Kind: "shipping",        Provider: "jnt",      Label: "J&T Express",               Fields: []ConnectorFieldDef{{Key: "api_key", Label: "API key", Secret: true}}},
	{Kind: "shipping",        Provider: "sicepat",  Label: "SiCepat",                   Fields: []ConnectorFieldDef{{Key: "api_key", Label: "API key", Secret: true}}},
	{Kind: "shipping",        Provider: "anteraja", Label: "Anteraja",                  Fields: []ConnectorFieldDef{{Key: "client_id", Label: "Client ID"}, {Key: "secret", Label: "Secret", Secret: true}}},
}

type ProviderDef struct {
	Kind     string     `json:"kind"`
	Provider string     `json:"provider"`
	Label    string     `json:"label"`
	Fields   []ConnectorFieldDef `json:"fields"`
}

type ConnectorFieldDef struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Secret bool   `json:"secret,omitempty"`
}

// ---- Types ------------------------------------------------------------------

type Connector struct {
	ID              string                 `json:"id"`
	Kind            string                 `json:"kind"`
	Provider        string                 `json:"provider"`
	Name            string                 `json:"name"`
	CompanyID       string                 `json:"company_id,omitempty"`
	IsEnabled       bool                   `json:"is_enabled"`
	TestMode        bool                   `json:"test_mode"`
	HasCredentials  bool                   `json:"has_credentials"`
	// Public config only — credentials never leave the server.
	Config          map[string]any         `json:"config,omitempty"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

type ConnectorInput struct {
	Kind        string                 `json:"kind"`
	Provider    string                 `json:"provider"`
	Name        string                 `json:"name"`
	CompanyID   string                 `json:"company_id,omitempty"`
	IsEnabled   *bool                  `json:"is_enabled,omitempty"`
	TestMode    *bool                  `json:"test_mode,omitempty"`
	Credentials map[string]string      `json:"credentials,omitempty" doc:"Omit to keep existing"`
	Config      map[string]any         `json:"config,omitempty"`
}

// ---- Service ----------------------------------------------------------------

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Providers() []ProviderDef { return ProviderCatalog }

func (s *Service) List(ctx context.Context, kind string) ([]Connector, error) {
	q := `
		SELECT id, kind, provider, name, coalesce(company_id,''), is_enabled, test_mode,
		       (credentials::text <> '{}'), config, updated_at
		FROM connector_config
		WHERE ($1 = '' OR kind = $1)
		ORDER BY kind, provider, name`
	rows, err := s.db.Query(ctx, q, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Connector, 0)
	for rows.Next() {
		var c Connector
		var rawConfig []byte
		if err := rows.Scan(&c.ID, &c.Kind, &c.Provider, &c.Name, &c.CompanyID, &c.IsEnabled, &c.TestMode,
			&c.HasCredentials, &rawConfig, &c.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(rawConfig, &c.Config)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) Upsert(ctx context.Context, id string, in ConnectorInput) (*Connector, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("connector: unauthenticated")
	}
	in.Kind = strings.TrimSpace(in.Kind)
	in.Provider = strings.TrimSpace(in.Provider)
	in.Name = strings.TrimSpace(in.Name)
	if in.Kind == "" || in.Provider == "" || in.Name == "" {
		return nil, errors.New("connector: kind, provider, name required")
	}
	if !validKind(in.Kind) {
		return nil, fmt.Errorf("connector.kind %q invalid", in.Kind)
	}
	enabled := false
	if in.IsEnabled != nil {
		enabled = *in.IsEnabled
	}
	testMode := true
	if in.TestMode != nil {
		testMode = *in.TestMode
	}

	if id == "" {
		id = dbx.NewIDWithPrefix("cnct")
	}

	// Merge incoming credentials with existing (so partial updates don't wipe).
	var existing map[string]string
	{
		var rawCred []byte
		_ = s.db.QueryRow(ctx, `SELECT credentials FROM connector_config WHERE id = $1`, id).Scan(&rawCred)
		if len(rawCred) > 0 {
			_ = json.Unmarshal(rawCred, &existing)
		}
	}
	if existing == nil {
		existing = map[string]string{}
	}
	for k, v := range in.Credentials {
		if v == "" {
			delete(existing, k) // empty string clears
		} else {
			existing[k] = v
		}
	}
	credBlob, _ := json.Marshal(existing)
	configBlob, _ := json.Marshal(in.Config)
	if string(configBlob) == "null" {
		configBlob = []byte("{}")
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO connector_config (id, kind, provider, name, company_id, is_enabled, test_mode,
		                              credentials, config, created_by, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,now())
		ON CONFLICT (id) DO UPDATE SET
		  name = EXCLUDED.name, is_enabled = EXCLUDED.is_enabled, test_mode = EXCLUDED.test_mode,
		  credentials = EXCLUDED.credentials, config = EXCLUDED.config,
		  updated_by = EXCLUDED.updated_by, updated_at = now()`,
		id, in.Kind, in.Provider, in.Name, nullable(in.CompanyID), enabled, testMode,
		credBlob, configBlob, p.UserID)
	if err != nil {
		if dbx.IsUniqueViolation(err) {
			return nil, errors.New("connector: a connector with (kind, provider, company, name) already exists")
		}
		return nil, err
	}
	return s.get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM connector_config WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("connector: not found")
	}
	return nil
}

func (s *Service) get(ctx context.Context, id string) (*Connector, error) {
	var c Connector
	var rawConfig []byte
	err := s.db.QueryRow(ctx, `
		SELECT id, kind, provider, name, coalesce(company_id,''), is_enabled, test_mode,
		       (credentials::text <> '{}'), config, updated_at
		FROM connector_config WHERE id = $1`, id).
		Scan(&c.ID, &c.Kind, &c.Provider, &c.Name, &c.CompanyID, &c.IsEnabled, &c.TestMode,
			&c.HasCredentials, &rawConfig, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(rawConfig, &c.Config)
	return &c, nil
}

func validKind(k string) bool {
	switch k {
	case "payment_gateway", "bank_feed", "marketplace", "shipping":
		return true
	}
	return false
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- HTTP -------------------------------------------------------------------

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-connector-providers", Method: http.MethodGet,
		Path: "/admin/connectors/providers", Summary: "List supported provider catalog",
		Tags: []string{"Admin / Connectors"},
	}, func(ctx context.Context, _ *struct{}) (*ccProvOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		return &ccProvOut{Body: ccProvBody{Items: h.Service.Providers()}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-connectors", Method: http.MethodGet,
		Path: "/admin/connectors", Summary: "List connector configs",
		Tags: []string{"Admin / Connectors"},
	}, func(ctx context.Context, in *ccListIn) (*ccListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		cs, err := h.Service.List(ctx, in.Kind)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &ccListOut{Body: ccListBody{Items: cs}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-connector", Method: http.MethodPost,
		Path: "/admin/connectors", Summary: "Create a connector config",
		Tags: []string{"Admin / Connectors"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *ccCreateIn) (*ccItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Upsert(ctx, "", in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &ccItemOut{Body: *c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-connector", Method: http.MethodPut,
		Path: "/admin/connectors/{id}", Summary: "Update a connector config",
		Tags: []string{"Admin / Connectors"},
	}, func(ctx context.Context, in *ccUpdateIn) (*ccItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Upsert(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &ccItemOut{Body: *c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-connector", Method: http.MethodDelete,
		Path: "/admin/connectors/{id}", Summary: "Delete a connector config",
		Tags: []string{"Admin / Connectors"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *ccByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})
}

type (
	ccProvOut  struct{ Body ccProvBody }
	ccProvBody struct {
		Items []ProviderDef `json:"items"`
	}
	ccListIn struct {
		Kind string `query:"kind" doc:"payment_gateway | bank_feed | marketplace | shipping"`
	}
	ccListOut  struct{ Body ccListBody }
	ccListBody struct {
		Items []Connector `json:"items"`
	}
	ccItemOut  struct{ Body Connector }
	ccByID     struct {
		ID string `path:"id"`
	}
	ccCreateIn struct{ Body ConnectorInput }
	ccUpdateIn struct {
		ID   string `path:"id"`
		Body ConnectorInput
	}
)
