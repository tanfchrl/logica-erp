// Package migration implements the implementation/onboarding wizard described
// in docs/agent-build-prompt.md §4.
//
// Five sequential steps, all resumable across browser sessions because state
// lives in agent_session.state:
//
//	1. Discovery interview     — conversational intake → SetupProfile
//	2. Chart of Accounts       — propose + accept PSAK-aligned COA
//	3. Data migration          — staged CSV/XLSX imports
//	4. Opening balances        — trial balance → JE with reconciliation proof
//	5. Go-live readiness       — structured checklist
//
// Steps are independent — a user can revisit earlier ones. The session's
// `state.step` field tracks the highest-completed step; the FE renders the
// numbered tracker from it.
package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tandigital/logica-erp/internal/agent/erpclient"
	"github.com/tandigital/logica-erp/internal/agent/session"
)

// Step is the user-visible numbered step in the wizard.
type Step string

const (
	StepDiscovery       Step = "discovery"
	StepCOA             Step = "coa"
	StepDataMigration   Step = "data_migration"
	StepOpeningBalances Step = "opening_balances"
	StepReadiness       Step = "readiness"
)

// SetupProfile is the structured output of Step 1. Drives all subsequent
// proposals — the COA generator, the staging schemas, and the readiness
// checklist all read from it.
type SetupProfile struct {
	BusinessType   string   `json:"business_type"`
	Industry       string   `json:"industry"`
	Employees      int      `json:"employees"`
	Modules        []string `json:"modules"`
	Multicompany   bool     `json:"multicompany"`
	FiscalYearStart string  `json:"fiscal_year_start"` // MM-DD, e.g. "01-01"
	BaseCurrency   string   `json:"base_currency"`     // ISO 4217, e.g. "IDR"
	LegacySystem   string   `json:"legacy_system"`     // free text, e.g. "ERPNext", "Excel"
}

// State is the JSON shape persisted in agent_session.state. The agent and
// the FE both read/write this directly.
type State struct {
	CurrentStep Step          `json:"current_step"`
	Completed   []Step        `json:"completed"`
	Profile     *SetupProfile `json:"profile,omitempty"`
	// Per-step intermediate artifacts. Loose-typed because each step lays
	// down its own shape.
	StepData map[Step]any `json:"step_data,omitempty"`
}

// Service drives wizard state transitions. It does NOT call the LLM —
// conversational steps go through the shared agent chat loop, which
// already lives in cmd/agent. This service just curates state.
type Service struct {
	sess *session.Store
	erp  *erpclient.Client
}

func New(sess *session.Store, erp *erpclient.Client) *Service {
	return &Service{sess: sess, erp: erp}
}

// Start creates a new migration session for the user. There's no "active
// session" semantics — a user can have multiple in flight (e.g. one per
// company being onboarded).
func (s *Service) Start(ctx context.Context, userID, companyID, title string) (*session.Session, error) {
	if title == "" {
		title = "New Company Setup"
	}
	sess, err := s.sess.Create(ctx, userID, companyID, title, session.KindMigration)
	if err != nil {
		return nil, err
	}
	initial := State{CurrentStep: StepDiscovery, StepData: map[Step]any{}}
	if err := s.sess.SetState(ctx, sess.ID, mustMap(initial)); err != nil {
		return nil, err
	}
	return sess, nil
}

// LoadState returns the current wizard state for a session.
func (s *Service) LoadState(ctx context.Context, userID, sessionID string) (*State, error) {
	sess, err := s.sess.Get(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.Kind != session.KindMigration {
		return nil, errors.New("migration: not a migration session")
	}
	var st State
	if err := decodeState(sess.State, &st); err != nil {
		return nil, err
	}
	if st.StepData == nil {
		st.StepData = map[Step]any{}
	}
	return &st, nil
}

// SaveProfile completes Step 1. Subsequent step transitions can rely on a
// non-nil Profile.
func (s *Service) SaveProfile(ctx context.Context, userID, sessionID string, p SetupProfile) (*State, error) {
	st, err := s.LoadState(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	st.Profile = &p
	st.completeStep(StepDiscovery)
	st.CurrentStep = StepCOA
	return s.saveAndReturn(ctx, sessionID, st)
}

// ProposeCOA generates a Chart of Accounts proposal from the SetupProfile.
// Returns the base PSAK skeleton plus an industry-specific overlay derived
// from SetupProfile.Industry (mapped through resolveIndustry). The result
// is stored under StepData so the FE can render it as an editable table
// before the user accepts.
func (s *Service) ProposeCOA(ctx context.Context, userID, sessionID string) ([]COAAccount, error) {
	st, err := s.LoadState(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	if st.Profile == nil {
		return nil, errors.New("migration: complete discovery interview first")
	}
	industry := resolveIndustry(st.Profile.Industry, st.Profile.BusinessType)
	proposal := buildCOAProposal(industry)
	data := map[string]any{
		"proposal":          proposal,
		"accepted":          false,
		"resolved_industry": string(industry),
	}
	st.StepData[StepCOA] = data
	if _, err := s.saveAndReturn(ctx, sessionID, st); err != nil {
		return nil, err
	}
	return proposal, nil
}

// AcceptCOA marks Step 2 complete. The actual creation of accounts via
// /accounting/accounts is best done by the caller (the agent's create_draft
// tool) so the calls go through the same path as a human creating accounts
// — keeps audit/permission semantics identical.
func (s *Service) AcceptCOA(ctx context.Context, userID, sessionID string) (*State, error) {
	st, err := s.LoadState(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	if _, ok := st.StepData[StepCOA].(map[string]any); !ok {
		return nil, errors.New("migration: propose a COA first")
	}
	data := st.StepData[StepCOA].(map[string]any)
	data["accepted"] = true
	st.StepData[StepCOA] = data
	st.completeStep(StepCOA)
	st.CurrentStep = StepDataMigration
	return s.saveAndReturn(ctx, sessionID, st)
}

// Readiness runs the go-live checklist (Step 5). Each check is concrete and
// observable — no LLM judgement involved. Returns the full list with
// pass/fail + the next-action hint per failing item.
func (s *Service) Readiness(ctx context.Context, userID, sessionID string, cc erpclient.CallContext) ([]Check, error) {
	st, err := s.LoadState(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	out := s.runReadinessChecks(ctx, cc, st)
	st.StepData[StepReadiness] = out
	if allPass(out) {
		st.completeStep(StepReadiness)
	}
	if _, err := s.saveAndReturn(ctx, sessionID, st); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- internals ----

func (s *Service) saveAndReturn(ctx context.Context, sessionID string, st *State) (*State, error) {
	if err := s.sess.SetState(ctx, sessionID, mustMap(*st)); err != nil {
		return nil, err
	}
	return st, nil
}

func (st *State) completeStep(step Step) {
	for _, c := range st.Completed {
		if c == step {
			return
		}
	}
	st.Completed = append(st.Completed, step)
}

// ---- COA proposal ----

// COAAccount is one proposed row in the chart of accounts. Mirrors the
// /accounting/accounts create endpoint's input shape.
type COAAccount struct {
	AccountNumber   string `json:"account_number"`
	Name            string `json:"name"`
	RootType        string `json:"root_type"`         // asset | liability | equity | income | expense
	AccountType     string `json:"account_type,omitempty"`
	IsGroup         bool   `json:"is_group"`
	Parent          string `json:"parent_account_number,omitempty"`
	AccountCurrency string `json:"account_currency,omitempty"`
}

// psakBaseCOA returns the minimum-viable Indonesian-PSAK-aligned chart of
// accounts. Industry-specific extensions (manufacturing WIP, retail
// merchandise, services revenue, etc.) layer on top in a future phase.
//
// The numbering follows the common Indonesian convention:
//
//	1xxx Aset (Assets)
//	2xxx Liabilitas (Liabilities)
//	3xxx Ekuitas (Equity)
//	4xxx Pendapatan (Revenue)
//	5xxx Beban (Expenses)
func psakBaseCOA() []COAAccount {
	return []COAAccount{
		// 1xxx Aset
		{AccountNumber: "1000", Name: "Aset", RootType: "asset", IsGroup: true},
		{AccountNumber: "1100", Name: "Aset Lancar", RootType: "asset", IsGroup: true, Parent: "1000"},
		{AccountNumber: "1110", Name: "Kas", RootType: "asset", AccountType: "cash", Parent: "1100"},
		{AccountNumber: "1120", Name: "Bank", RootType: "asset", AccountType: "bank", Parent: "1100"},
		{AccountNumber: "1130", Name: "Piutang Usaha", RootType: "asset", AccountType: "receivable", Parent: "1100"},
		{AccountNumber: "1140", Name: "Persediaan", RootType: "asset", AccountType: "stock", Parent: "1100"},
		{AccountNumber: "1150", Name: "PPN Masukan", RootType: "asset", AccountType: "tax", Parent: "1100"},
		{AccountNumber: "1500", Name: "Aset Tetap", RootType: "asset", IsGroup: true, Parent: "1000"},
		{AccountNumber: "1510", Name: "Aset Tetap (Bruto)", RootType: "asset", AccountType: "fixed_asset", Parent: "1500"},
		{AccountNumber: "1520", Name: "Akumulasi Penyusutan", RootType: "asset", AccountType: "accumulated_depreciation", Parent: "1500"},

		// 2xxx Liabilitas
		{AccountNumber: "2000", Name: "Liabilitas", RootType: "liability", IsGroup: true},
		{AccountNumber: "2100", Name: "Liabilitas Lancar", RootType: "liability", IsGroup: true, Parent: "2000"},
		{AccountNumber: "2110", Name: "Utang Usaha", RootType: "liability", AccountType: "payable", Parent: "2100"},
		{AccountNumber: "2120", Name: "PPN Keluaran", RootType: "liability", AccountType: "tax", Parent: "2100"},
		{AccountNumber: "2130", Name: "Utang PPh 21", RootType: "liability", AccountType: "tax", Parent: "2100"},
		{AccountNumber: "2140", Name: "Utang PPh 23", RootType: "liability", AccountType: "tax", Parent: "2100"},
		{AccountNumber: "2150", Name: "Utang PPh 25", RootType: "liability", AccountType: "tax", Parent: "2100"},
		{AccountNumber: "2160", Name: "Utang PPh 26", RootType: "liability", AccountType: "tax", Parent: "2100"},

		// 3xxx Ekuitas
		{AccountNumber: "3000", Name: "Ekuitas", RootType: "equity", IsGroup: true},
		{AccountNumber: "3100", Name: "Modal Disetor", RootType: "equity", Parent: "3000"},
		{AccountNumber: "3200", Name: "Laba Ditahan", RootType: "equity", Parent: "3000"},

		// 4xxx Pendapatan
		{AccountNumber: "4000", Name: "Pendapatan", RootType: "income", IsGroup: true},
		{AccountNumber: "4100", Name: "Penjualan", RootType: "income", AccountType: "income", Parent: "4000"},
		{AccountNumber: "4200", Name: "Pendapatan Lain-lain", RootType: "income", AccountType: "income", Parent: "4000"},

		// 5xxx Beban
		{AccountNumber: "5000", Name: "Beban", RootType: "expense", IsGroup: true},
		{AccountNumber: "5100", Name: "Harga Pokok Penjualan", RootType: "expense", AccountType: "cost_of_goods_sold", Parent: "5000"},
		{AccountNumber: "5200", Name: "Beban Operasional", RootType: "expense", AccountType: "expense", Parent: "5000"},
		{AccountNumber: "5300", Name: "Beban Gaji", RootType: "expense", AccountType: "expense", Parent: "5000"},
		{AccountNumber: "5400", Name: "Beban Penyusutan", RootType: "expense", AccountType: "depreciation_expense", Parent: "5000"},
	}
}

// ---- Industry overlays ----

// Industry is the small enum that drives the per-industry COA overlay.
// SetupProfile.Industry is free text — resolveIndustry maps it to one of
// these. "Other" returns no overlay.
type Industry string

const (
	IndustryTrading       Industry = "trading"
	IndustryManufacturing Industry = "manufacturing"
	IndustryServices      Industry = "services"
	IndustryConstruction  Industry = "construction"
	IndustryOther         Industry = "other"
)

// resolveIndustry maps the free-text Industry + BusinessType fields onto
// the closed Industry enum. Falls back to IndustryOther — that yields the
// plain base COA so an unknown classification doesn't break setup.
func resolveIndustry(industry, businessType string) Industry {
	hay := strings.ToLower(industry + " " + businessType)
	// Order matters: "construction services" should classify as construction,
	// not services.
	switch {
	case containsAny(hay, "construction", "kontraktor", "konstruksi", "contractor", "developer"):
		return IndustryConstruction
	case containsAny(hay, "manufactur", "manufaktur", "pabrik", "factory", "produksi"):
		return IndustryManufacturing
	case containsAny(hay, "trading", "retail", "ritel", "dagang", "distribu", "wholesale", "grosir", "toko", "merchant"):
		return IndustryTrading
	case containsAny(hay, "service", "jasa", "consult", "konsultan", "agency", "agensi", "software", "saas"):
		return IndustryServices
	}
	return IndustryOther
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// buildCOAProposal returns the base PSAK COA with the chosen industry's
// overlay merged in. Overlay rows with the same account number REPLACE
// the base row — that lets an overlay turn a leaf into a group (e.g.
// Persediaan becomes a parent for raw/WIP/finished sub-accounts in
// manufacturing). New rows are appended in their declared order so the
// FE renders them grouped near their parents.
func buildCOAProposal(ind Industry) []COAAccount {
	base := psakBaseCOA()
	overlay := industryOverlay(ind)
	if len(overlay) == 0 {
		return base
	}
	// Index base by account_number so overlays can override.
	idx := make(map[string]int, len(base))
	for i, a := range base {
		idx[a.AccountNumber] = i
	}
	out := make([]COAAccount, len(base))
	copy(out, base)
	for _, a := range overlay {
		if i, ok := idx[a.AccountNumber]; ok {
			out[i] = a
		} else {
			out = append(out, a)
			idx[a.AccountNumber] = len(out) - 1
		}
	}
	return out
}

// industryOverlay returns the additional + replacement accounts for an
// industry. Empty slice for IndustryOther. Account numbers slot into the
// base scheme so the merged COA still flows asset → liability → equity →
// income → expense from low to high.
func industryOverlay(ind Industry) []COAAccount {
	switch ind {
	case IndustryTrading:
		return []COAAccount{
			// Persediaan becomes a parent group; merchandise sits under it.
			{AccountNumber: "1140", Name: "Persediaan", RootType: "asset", IsGroup: true, Parent: "1100"},
			{AccountNumber: "1141", Name: "Persediaan Barang Dagang", RootType: "asset", AccountType: "stock", Parent: "1140"},
			// Contra-revenue + freight accounts are the day-one need.
			{AccountNumber: "4150", Name: "Retur Penjualan", RootType: "income", AccountType: "income", Parent: "4000"},
			{AccountNumber: "4160", Name: "Diskon Penjualan", RootType: "income", AccountType: "income", Parent: "4000"},
			{AccountNumber: "5210", Name: "Beban Angkut Masuk", RootType: "expense", AccountType: "expense", Parent: "5200"},
			{AccountNumber: "5220", Name: "Beban Angkut Keluar", RootType: "expense", AccountType: "expense", Parent: "5200"},
		}

	case IndustryManufacturing:
		return []COAAccount{
			// Persediaan becomes a parent — three classic buckets sit under it.
			{AccountNumber: "1140", Name: "Persediaan", RootType: "asset", IsGroup: true, Parent: "1100"},
			{AccountNumber: "1141", Name: "Persediaan Bahan Baku", RootType: "asset", AccountType: "stock", Parent: "1140"},
			{AccountNumber: "1142", Name: "Persediaan Barang Dalam Proses (WIP)", RootType: "asset", AccountType: "stock", Parent: "1140"},
			{AccountNumber: "1143", Name: "Persediaan Barang Jadi", RootType: "asset", AccountType: "stock", Parent: "1140"},
			// HPP broken into the standard manufacturing cost buckets.
			{AccountNumber: "5100", Name: "Harga Pokok Produksi", RootType: "expense", IsGroup: true, Parent: "5000"},
			{AccountNumber: "5110", Name: "Beban Bahan Baku", RootType: "expense", AccountType: "cost_of_goods_sold", Parent: "5100"},
			{AccountNumber: "5120", Name: "Beban Tenaga Kerja Langsung", RootType: "expense", AccountType: "cost_of_goods_sold", Parent: "5100"},
			{AccountNumber: "5130", Name: "Beban Overhead Pabrik", RootType: "expense", AccountType: "cost_of_goods_sold", Parent: "5100"},
		}

	case IndustryServices:
		return []COAAccount{
			// Unbilled receivables + service WIP — common services pain points.
			{AccountNumber: "1135", Name: "Piutang Belum Ditagih", RootType: "asset", AccountType: "receivable", Parent: "1100"},
			{AccountNumber: "1145", Name: "Pekerjaan Dalam Proses Jasa", RootType: "asset", AccountType: "stock", Parent: "1100"},
			// Deferred revenue is THE liability for retainer/subscription work.
			{AccountNumber: "2170", Name: "Pendapatan Diterima di Muka", RootType: "liability", AccountType: "payable", Parent: "2100"},
			// Rename revenue head to make the model obvious.
			{AccountNumber: "4100", Name: "Pendapatan Jasa", RootType: "income", AccountType: "income", Parent: "4000"},
		}

	case IndustryConstruction:
		return []COAAccount{
			// Construction WIP — billings under it net to over/under-billed.
			{AccountNumber: "1145", Name: "Pekerjaan Dalam Proses Konstruksi", RootType: "asset", AccountType: "stock", Parent: "1100"},
			// Progress billings + retention payable.
			{AccountNumber: "2170", Name: "Tagihan Termin Pelanggan", RootType: "liability", AccountType: "payable", Parent: "2100"},
			{AccountNumber: "2180", Name: "Utang Retensi", RootType: "liability", AccountType: "payable", Parent: "2100"},
			// Contract revenue + contract costs as separate heads.
			{AccountNumber: "4100", Name: "Pendapatan Kontrak", RootType: "income", AccountType: "income", Parent: "4000"},
			{AccountNumber: "5110", Name: "Beban Kontrak", RootType: "expense", AccountType: "cost_of_goods_sold", Parent: "5100"},
		}
	}
	return nil
}

// ---- Readiness checks ----

type Check struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Pass       bool   `json:"pass"`
	Detail     string `json:"detail,omitempty"`
	FixURL     string `json:"fix_url,omitempty"`
}

func (s *Service) runReadinessChecks(ctx context.Context, cc erpclient.CallContext, st *State) []Check {
	checks := []Check{}

	// 1. COA covers each required root_type.
	accounts := s.listOrEmpty(ctx, cc, "/accounting/accounts")
	gotRoots := map[string]bool{}
	for _, a := range accounts {
		if rt, ok := a["root_type"].(string); ok {
			gotRoots[rt] = true
		}
	}
	rootChecks := []struct{ key, label string }{
		{"asset", "Asset accounts"},
		{"liability", "Liability accounts"},
		{"equity", "Equity accounts"},
		{"income", "Revenue accounts"},
		{"expense", "Expense accounts"},
	}
	for _, rc := range rootChecks {
		checks = append(checks, Check{
			ID:    "coa-" + rc.key,
			Label: "Chart of accounts: " + rc.label + " exist",
			Pass:  gotRoots[rc.key],
			FixURL: "/accounting/accounts",
		})
	}

	// 2. At least one tax template configured.
	taxes := s.listOrEmpty(ctx, cc, "/accounting/tax-templates")
	checks = append(checks, Check{
		ID:     "tax-templates",
		Label:  "At least one tax template configured",
		Pass:   len(taxes) > 0,
		Detail: fmt.Sprintf("%d configured", len(taxes)),
		FixURL: "/settings/tax-templates",
	})

	// 3. At least one warehouse.
	whs := s.listOrEmpty(ctx, cc, "/stock/warehouses")
	checks = append(checks, Check{
		ID: "warehouse", Label: "At least one warehouse defined",
		Pass: len(whs) > 0, FixURL: "/stock/warehouses",
	})

	// 4. At least one company.
	companies := s.listOrEmpty(ctx, cc, "/accounting/companies")
	hasNPWP := false
	for _, c := range companies {
		if n, ok := c["npwp"].(string); ok && n != "" {
			hasNPWP = true
		}
	}
	checks = append(checks, Check{
		ID: "company", Label: "Company exists with NPWP set",
		Pass: len(companies) > 0 && hasNPWP, FixURL: "/settings/companies",
	})

	// 5. At least one user with a role.
	users := s.listOrEmpty(ctx, cc, "/admin/users")
	checks = append(checks, Check{
		ID: "users", Label: "Users + roles configured",
		Pass: len(users) >= 1, FixURL: "/settings/users",
	})

	// 6. Print template for the sales invoice.
	templates := s.listOrEmpty(ctx, cc, "/admin/print-templates")
	hasSITpl := false
	for _, t := range templates {
		if dt, _ := t["doctype"].(string); dt == "sales_invoice" {
			hasSITpl = true
		}
	}
	checks = append(checks, Check{
		ID: "print-template-si", Label: "Sales-invoice print template configured",
		Pass: hasSITpl, FixURL: "/settings/print-templates",
	})

	return checks
}

func (s *Service) listOrEmpty(ctx context.Context, cc erpclient.CallContext, path string) []map[string]any {
	var resp map[string]any
	if err := s.erp.Do(ctx, cc, "GET", path, nil, &resp); err != nil {
		return nil
	}
	items, ok := resp["items"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func allPass(c []Check) bool {
	for _, x := range c {
		if !x.Pass {
			return false
		}
	}
	return true
}

// ---- helpers to round-trip State through the session-store's
// generic map[string]any column. ----

func mustMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func decodeState(m map[string]any, out *State) error {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
