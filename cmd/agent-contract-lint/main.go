// agent-contract-lint walks the working directory looking for AGENT_CONTRACT.md
// files and validates each against the schema in internal/agentcontract.
//
// Wire into CI as:  go run ./cmd/agent-contract-lint
// Wire into Make as: make agent-contract-lint
package main

import (
	"fmt"
	"os"

	"github.com/tandigital/logica-erp/internal/agentcontract"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	fails := agentcontract.LintFS(os.DirFS(root), ".")
	if len(fails) == 0 {
		reg, err := agentcontract.LoadFS(os.DirFS(root), ".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "lint: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("agent-contract-lint OK:", reg.Summary())
		return
	}
	for _, f := range fails {
		fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", f.Path, f.Err)
	}
	os.Exit(1)
}
