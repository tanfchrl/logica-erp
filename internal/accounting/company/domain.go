// Package company implements the Company master.
package company

import "time"

const Doctype = "company"

type Company struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	LegalName       string    `json:"legal_name"`
	Abbreviation    string    `json:"abbreviation"`
	Country         string    `json:"country"`
	DefaultCurrency string    `json:"default_currency"`
	NPWP            string    `json:"npwp,omitempty"`
	NPWPAddress     string    `json:"npwp_address,omitempty"`
	AddressLine     string    `json:"address_line,omitempty"`
	City            string    `json:"city,omitempty"`
	Province        string    `json:"province,omitempty"`
	PostalCode      string    `json:"postal_code,omitempty"`
	Phone           string    `json:"phone,omitempty"`
	Email           string    `json:"email,omitempty"`
	Website         string    `json:"website,omitempty"`
	IsDeleted       bool      `json:"is_deleted"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
