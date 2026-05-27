package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// runBackup invokes pg_dump and writes a gzipped SQL file.
func runBackup(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: logica backup <output.sql.gz>")
		os.Exit(2)
	}
	out := args[0]
	cfg := mustConfig()
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse db url:", err)
		os.Exit(1)
	}
	pgArgs := []string{
		"--host=" + u.Hostname(),
		"--port=" + portOrDefault(u, "5432"),
		"--username=" + u.User.Username(),
		"--no-password",
		"--format=plain",
		"--no-owner",
		"--no-privileges",
		strings.TrimPrefix(u.Path, "/"),
	}
	cmd := exec.Command("pg_dump", pgArgs...)
	if pw, ok := u.User.Password(); ok {
		cmd.Env = append(os.Environ(), "PGPASSWORD="+pw)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd.Stderr = os.Stderr

	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()

	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := io.Copy(gz, stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := cmd.Wait(); err != nil {
		fmt.Fprintln(os.Stderr, "pg_dump failed:", err)
		os.Exit(1)
	}
	fmt.Println("backup written to", out)
}

// runRestore gunzips the input and replays through psql.
func runRestore(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: logica restore <input.sql.gz>")
		os.Exit(2)
	}
	in := args[0]
	cfg := mustConfig()
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f, err := os.Open(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer gz.Close()

	cmd := exec.Command("psql",
		"--host="+u.Hostname(),
		"--port="+portOrDefault(u, "5432"),
		"--username="+u.User.Username(),
		"--no-password",
		"--dbname="+strings.TrimPrefix(u.Path, "/"),
	)
	if pw, ok := u.User.Password(); ok {
		cmd.Env = append(os.Environ(), "PGPASSWORD="+pw)
	}
	cmd.Stdin = gz
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "psql restore failed:", err)
		os.Exit(1)
	}
	fmt.Println("restored from", in)
}

func portOrDefault(u *url.URL, def string) string {
	if p := u.Port(); p != "" {
		return p
	}
	return def
}
