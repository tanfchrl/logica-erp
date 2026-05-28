// Package customfield validates user-supplied custom field payloads against
// custom_field_definition rows for the target doctype.
package customfield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

type FieldType string

const (
	TypeText     FieldType = "text"
	TypeInt      FieldType = "int"
	TypeDecimal  FieldType = "decimal"
	TypeDate     FieldType = "date"
	TypeDatetime FieldType = "datetime"
	TypeBool     FieldType = "bool"
	TypeSelect   FieldType = "select"
	TypeLink     FieldType = "link"
	TypeTable    FieldType = "table"
)

type Definition struct {
	FieldName    string
	FieldType    FieldType
	IsRequired   bool
	DefaultValue *string
	// Parsed options. For TypeSelect, SelectValues holds the allowed strings;
	// for TypeLink, LinkDoctype constrains the link target.
	SelectValues []string
	LinkDoctype  string
}

// optionsShape mirrors the admin-side schema documented on FieldDefInput.Options.
type optionsShape struct {
	Values  []string `json:"values,omitempty"`
	Doctype string   `json:"doctype,omitempty"`
}

func parseOptions(optsJSON []byte) (selectValues []string, linkDoctype string, err error) {
	if len(optsJSON) == 0 {
		return nil, "", nil
	}
	var o optionsShape
	if err := json.Unmarshal(optsJSON, &o); err != nil {
		return nil, "", fmt.Errorf("custom_field_definition.options: invalid JSON: %w", err)
	}
	return o.Values, o.Doctype, nil
}

type Validator struct {
	db *dbx.DB
}

func NewValidator(db *dbx.DB) *Validator {
	return &Validator{db: db}
}

// Validate normalises and validates the payload for the given doctype.
// Unknown keys are rejected. Missing required keys are rejected.
func (v *Validator) Validate(ctx context.Context, doctype string, payload map[string]any) (map[string]any, error) {
	defs, err := v.definitionsFor(ctx, doctype)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	seen := map[string]bool{}

	for key, raw := range payload {
		def, ok := defs[key]
		if !ok {
			return nil, fmt.Errorf("custom_fields: unknown field %q for %s", key, doctype)
		}
		v, err := coerce(def, raw)
		if err != nil {
			return nil, fmt.Errorf("custom_fields.%s: %w", key, err)
		}
		out[key] = v
		seen[key] = true
	}

	for name, def := range defs {
		if def.IsRequired && !seen[name] {
			return nil, fmt.Errorf("custom_fields.%s: required", name)
		}
	}
	return out, nil
}

func (v *Validator) definitionsFor(ctx context.Context, doctype string) (map[string]Definition, error) {
	rows, err := v.db.Query(ctx, `
		SELECT field_name, field_type, is_required, default_value, options
		FROM custom_field_definition WHERE doctype = $1`, doctype)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Definition{}
	for rows.Next() {
		var (
			d        Definition
			optsJSON []byte
		)
		if err := rows.Scan(&d.FieldName, &d.FieldType, &d.IsRequired, &d.DefaultValue, &optsJSON); err != nil {
			return nil, err
		}
		sv, ld, err := parseOptions(optsJSON)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", doctype, d.FieldName, err)
		}
		d.SelectValues, d.LinkDoctype = sv, ld
		out[d.FieldName] = d
	}
	return out, rows.Err()
}

func coerce(def Definition, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	switch def.FieldType {
	case TypeText:
		if s, ok := raw.(string); ok {
			return s, nil
		}
	case TypeSelect:
		s, ok := raw.(string)
		if !ok {
			break
		}
		if len(def.SelectValues) == 0 {
			// No constraint defined — accept any string.
			return s, nil
		}
		for _, v := range def.SelectValues {
			if v == s {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%q not in allowed values %v", s, def.SelectValues)
	case TypeInt:
		switch x := raw.(type) {
		case int, int32, int64:
			return x, nil
		case float64:
			if x != float64(int64(x)) {
				return nil, errors.New("not an integer")
			}
			return int64(x), nil
		case string:
			n, err := strconv.ParseInt(x, 10, 64)
			if err != nil {
				return nil, err
			}
			return n, nil
		}
	case TypeDecimal:
		if s, ok := raw.(string); ok {
			return s, nil
		}
		if f, ok := raw.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64), nil
		}
	case TypeBool:
		if b, ok := raw.(bool); ok {
			return b, nil
		}
	case TypeDate:
		if s, ok := raw.(string); ok {
			if _, err := time.Parse("2006-01-02", s); err != nil {
				return nil, err
			}
			return s, nil
		}
	case TypeDatetime:
		if s, ok := raw.(string); ok {
			if _, err := time.Parse(time.RFC3339, s); err != nil {
				return nil, err
			}
			return s, nil
		}
	case TypeLink:
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New(`expected {"type":"...", "id":"..."}`)
		}
		t, _ := m["type"].(string)
		id, _ := m["id"].(string)
		if t == "" || id == "" {
			return nil, errors.New(`expected {"type":"...", "id":"..."}`)
		}
		if def.LinkDoctype != "" && t != def.LinkDoctype {
			return nil, fmt.Errorf("link type %q not allowed (expected %q)", t, def.LinkDoctype)
		}
		return m, nil
	case TypeTable:
		if a, ok := raw.([]any); ok {
			return a, nil
		}
	}
	return nil, fmt.Errorf("invalid value for %s", def.FieldType)
}

// EnsureTxValidator validates payload using the supplied tx (for read consistency).
// Kept as a free function so it doesn't depend on the pool.
func EnsureTxValidator(ctx context.Context, tx pgx.Tx, doctype string, payload map[string]any) (map[string]any, error) {
	rows, err := tx.Query(ctx, `
		SELECT field_name, field_type, is_required, default_value, options
		FROM custom_field_definition WHERE doctype = $1`, doctype)
	if err != nil {
		return nil, err
	}
	defs := map[string]Definition{}
	for rows.Next() {
		var (
			d        Definition
			optsJSON []byte
		)
		if err := rows.Scan(&d.FieldName, &d.FieldType, &d.IsRequired, &d.DefaultValue, &optsJSON); err != nil {
			rows.Close()
			return nil, err
		}
		sv, ld, err := parseOptions(optsJSON)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("%s.%s: %w", doctype, d.FieldName, err)
		}
		d.SelectValues, d.LinkDoctype = sv, ld
		defs[d.FieldName] = d
	}
	rows.Close()

	out := map[string]any{}
	seen := map[string]bool{}
	for k, v := range payload {
		def, ok := defs[k]
		if !ok {
			return nil, fmt.Errorf("custom_fields: unknown field %q for %s", k, doctype)
		}
		val, err := coerce(def, v)
		if err != nil {
			return nil, fmt.Errorf("custom_fields.%s: %w", k, err)
		}
		out[k] = val
		seen[k] = true
	}
	for name, def := range defs {
		if def.IsRequired && !seen[name] {
			return nil, fmt.Errorf("custom_fields.%s: required", name)
		}
	}
	return out, nil
}
