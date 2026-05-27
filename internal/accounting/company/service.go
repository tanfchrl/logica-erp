package company

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

var npwpRegex = regexp.MustCompile(`^[0-9]{16}$`)

type Service struct {
	db *dbx.DB
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

type CompanyCreateInput struct {
	Name            string         `json:"name"`
	LegalName       string         `json:"legal_name"`
	Abbreviation    string         `json:"abbreviation"`
	Country         string         `json:"country,omitempty"`
	DefaultCurrency string         `json:"default_currency,omitempty"`
	NPWP            string         `json:"npwp,omitempty"`
	NPWPAddress     string         `json:"npwp_address,omitempty"`
	AddressLine     string         `json:"address_line,omitempty"`
	City            string         `json:"city,omitempty"`
	Province        string         `json:"province,omitempty"`
	PostalCode      string         `json:"postal_code,omitempty"`
	Phone           string         `json:"phone,omitempty"`
	Email           string         `json:"email,omitempty"`
	Website         string         `json:"website,omitempty"`
	CustomFields    map[string]any `json:"custom_fields,omitempty"`
	// GrantToCurrentUser, when true, links the creating user to this company so they can access it.
	GrantToCurrentUser bool `json:"grant_to_current_user,omitempty"`
}

func (s *Service) Create(ctx context.Context, in CompanyCreateInput) (*Company, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("company: unauthenticated")
	}
	if err := validateCreate(&in); err != nil {
		return nil, err
	}

	id := dbx.NewIDWithPrefix("cmp")
	var c Company
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO company (
				id, name, legal_name, abbreviation, country, default_currency,
				npwp, npwp_address, address_line, city, province, postal_code,
				phone, email, website, custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			RETURNING id, name, legal_name, abbreviation, country, default_currency,
			          coalesce(npwp,''), coalesce(npwp_address,''), coalesce(address_line,''),
			          coalesce(city,''), coalesce(province,''), coalesce(postal_code,''),
			          coalesce(phone,''), coalesce(email,''), coalesce(website,''),
			          is_deleted, created_at, updated_at`,
			id, in.Name, in.LegalName, strings.ToUpper(in.Abbreviation),
			defaulted(in.Country, "ID"), defaulted(in.DefaultCurrency, "IDR"),
			nullable(in.NPWP), nullable(in.NPWPAddress), nullable(in.AddressLine),
			nullable(in.City), nullable(in.Province), nullable(in.PostalCode),
			nullable(in.Phone), nullable(in.Email), nullable(in.Website),
			cf, p.UserID, p.UserID,
		)
		if err := row.Scan(&c.ID, &c.Name, &c.LegalName, &c.Abbreviation, &c.Country, &c.DefaultCurrency,
			&c.NPWP, &c.NPWPAddress, &c.AddressLine, &c.City, &c.Province, &c.PostalCode,
			&c.Phone, &c.Email, &c.Website, &c.IsDeleted, &c.CreatedAt, &c.UpdatedAt); err != nil {
			if dbx.IsUniqueViolation(err) {
				return fmt.Errorf("company: duplicate name or abbreviation")
			}
			return err
		}
		if in.GrantToCurrentUser {
			if _, err := tx.Exec(ctx,
				`INSERT INTO user_company (user_id, company_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
				p.UserID, c.ID); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, Doctype, c.ID, p.UserID, audit.ActionCreate, audit.Diff{After: c})
	})
	return &c, err
}

func (s *Service) Get(ctx context.Context, id string) (*Company, error) {
	var c Company
	err := s.db.QueryRow(ctx, `
		SELECT id, name, legal_name, abbreviation, country, default_currency,
		       coalesce(npwp,''), coalesce(npwp_address,''), coalesce(address_line,''),
		       coalesce(city,''), coalesce(province,''), coalesce(postal_code,''),
		       coalesce(phone,''), coalesce(email,''), coalesce(website,''),
		       is_deleted, created_at, updated_at
		FROM company WHERE id = $1 AND is_deleted = false`, id).
		Scan(&c.ID, &c.Name, &c.LegalName, &c.Abbreviation, &c.Country, &c.DefaultCurrency,
			&c.NPWP, &c.NPWPAddress, &c.AddressLine, &c.City, &c.Province, &c.PostalCode,
			&c.Phone, &c.Email, &c.Website, &c.IsDeleted, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("company %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) List(ctx context.Context) ([]Company, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("company: unauthenticated")
	}
	args := []any{}
	where := "is_deleted = false"
	if !p.IsSystem {
		if len(p.Companies) == 0 {
			return []Company{}, nil
		}
		args = append(args, p.Companies)
		where += " AND id = ANY($1)"
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, name, legal_name, abbreviation, country, default_currency,
		       coalesce(npwp,''), coalesce(npwp_address,''), coalesce(address_line,''),
		       coalesce(city,''), coalesce(province,''), coalesce(postal_code,''),
		       coalesce(phone,''), coalesce(email,''), coalesce(website,''),
		       is_deleted, created_at, updated_at
		FROM company WHERE `+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Company
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.ID, &c.Name, &c.LegalName, &c.Abbreviation, &c.Country, &c.DefaultCurrency,
			&c.NPWP, &c.NPWPAddress, &c.AddressLine, &c.City, &c.Province, &c.PostalCode,
			&c.Phone, &c.Email, &c.Website, &c.IsDeleted, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func validateCreate(in *CompanyCreateInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.LegalName = strings.TrimSpace(in.LegalName)
	in.Abbreviation = strings.TrimSpace(in.Abbreviation)
	in.NPWP = strings.TrimSpace(in.NPWP)
	if in.Name == "" {
		return errors.New("company.name: required")
	}
	if in.LegalName == "" {
		return errors.New("company.legal_name: required")
	}
	if in.Abbreviation == "" {
		return errors.New("company.abbreviation: required")
	}
	if in.NPWP != "" && !npwpRegex.MatchString(in.NPWP) {
		return errors.New("company.npwp: must be 16 digits")
	}
	return nil
}

func defaulted(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
