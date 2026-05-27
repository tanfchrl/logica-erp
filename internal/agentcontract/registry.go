package agentcontract

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/httpx"
)

// Registry holds the merged contracts loaded from disk at startup. Exposed
// via GET /api/v1/agent/contracts so the agent service can fetch them.
type Registry struct {
	contracts []Contract
}

// LoadFS walks the given filesystem looking for `AGENT_CONTRACT.md` under any
// directory and parses each. Designed for use with go:embed or os.DirFS so the
// same code runs in tests and production.
func LoadFS(fsys fs.FS, root string) (*Registry, error) {
	var contracts []Contract
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "AGENT_CONTRACT.md" {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		c, err := Parse(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		contracts = append(contracts, *c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Deterministic order — useful for the lint CI and for the agent's
	// system-prompt construction.
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].Module < contracts[j].Module })
	return &Registry{contracts: contracts}, nil
}

// NewRegistry builds a Registry from an in-memory slice of contracts. Used
// by tests; production callers prefer LoadFS so the actual AGENT_CONTRACT.md
// files are the source of truth.
func NewRegistry(contracts []Contract) *Registry {
	cp := append([]Contract(nil), contracts...)
	return &Registry{contracts: cp}
}

// All returns the loaded contracts. Caller must not mutate.
func (r *Registry) All() []Contract { return r.contracts }

// FindByDoctype returns the module + document spec that owns a given doctype,
// or false if no contract declares it.
func (r *Registry) FindByDoctype(doctype string) (Contract, DocumentSpec, bool) {
	for _, c := range r.contracts {
		for _, d := range c.Documents {
			if d.Name == doctype {
				return c, d, true
			}
		}
	}
	return Contract{}, DocumentSpec{}, false
}

// ---- HTTP ----

// Handler registers GET /agent/contracts. The endpoint is public to any
// authenticated user — contract metadata doesn't expose business data.
type Handler struct {
	Registry *Registry
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-agent-contracts",
		Method:      http.MethodGet,
		Path:        "/agent/contracts",
		Summary:     "Merged AGENT_CONTRACT.md contracts for all modules",
		Tags:        []string{"Agent / Contracts"},
	}, func(ctx context.Context, _ *struct{}) (*contractsOut, error) {
		if h.Registry == nil {
			return nil, httpx.MapError(errors.New("agent contracts registry not initialised"))
		}
		return &contractsOut{Body: contractsBody{Items: h.Registry.All()}}, nil
	})
}

// LintFS is the entry point used by `make agent-contract-lint`. It parses
// every contract under root and returns the list of (path, err) for failures.
// Empty slice means clean.
type LintFailure struct {
	Path string
	Err  error
}

func LintFS(fsys fs.FS, root string) []LintFailure {
	var fails []LintFailure
	_ = fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fails = append(fails, LintFailure{Path: path, Err: err})
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "AGENT_CONTRACT.md" {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			fails = append(fails, LintFailure{Path: path, Err: err})
			return nil
		}
		if _, err := Parse(data); err != nil {
			fails = append(fails, LintFailure{Path: path, Err: err})
		}
		return nil
	})
	return fails
}

// Summary returns a human-readable count for use in the lint CLI output.
func (r *Registry) Summary() string {
	mods := make([]string, len(r.contracts))
	for i, c := range r.contracts {
		mods[i] = c.Module
	}
	return fmt.Sprintf("%d contracts: %s", len(mods), strings.Join(mods, ", "))
}

type (
	contractsOut  struct{ Body contractsBody }
	contractsBody struct {
		Items []Contract `json:"items"`
	}
)
