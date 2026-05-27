// Package naming implements ERPNext-style naming series.
//
// Patterns use dot-delimited placeholders inside dots: "INV-.YYYY.-.####"
// produces "INV-2026-0001". Supported placeholders:
//
//	.YYYY.   four-digit year (uses fiscal year start year if a FY is provided to Resolve)
//	.YY.     two-digit year
//	.MM.     two-digit month
//	.DD.     two-digit day
//	.####    counter, padded to the run-length of hashes (e.g. "####" => 4 digits, "######" => 6)
//	.{name}. literal lookup on the resolver map (e.g. ".company_abbr." -> company abbreviation)
//
// Counters are persisted per (series_id, scope_key) where scope_key concatenates the
// resolved values of all non-counter placeholders. This ensures year/period rollover.
package naming

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Resolver func(placeholder string) (string, bool)

// Next returns the next document name for a series.
// The counter row is upserted and incremented atomically inside the caller's transaction.
func Next(ctx context.Context, tx pgx.Tx, seriesID, pattern string, now time.Time, resolve Resolver) (string, error) {
	parts, scopeKey, counterWidth, err := compile(pattern, now, resolve)
	if err != nil {
		return "", err
	}
	if counterWidth == 0 {
		return strings.Join(parts, ""), nil
	}

	var next int64
	err = tx.QueryRow(ctx, `
		INSERT INTO naming_series_counter (series_id, scope_key, current_value)
		VALUES ($1,$2,1)
		ON CONFLICT (series_id, scope_key)
		DO UPDATE SET current_value = naming_series_counter.current_value + 1
		RETURNING current_value`, seriesID, scopeKey).Scan(&next)
	if err != nil {
		return "", fmt.Errorf("naming: counter: %w", err)
	}

	for i, p := range parts {
		if p == "\x00COUNTER" {
			parts[i] = fmt.Sprintf("%0*d", counterWidth, next)
			break
		}
	}
	return strings.Join(parts, ""), nil
}

// compile splits the pattern into rendered string parts (with a sentinel for the counter),
// returns the scope key (used as the counter partition) and the counter width.
func compile(pattern string, now time.Time, resolve Resolver) ([]string, string, int, error) {
	var (
		out          []string
		scope        strings.Builder
		counterWidth int
		hadCounter   bool
	)
	rest := pattern
	for {
		i := strings.Index(rest, ".")
		if i < 0 {
			if rest != "" {
				out = append(out, rest)
			}
			break
		}
		if i > 0 {
			out = append(out, rest[:i])
		}
		rest = rest[i+1:]
		// find closing dot OR end-of-string if pattern ends with .####
		end := strings.Index(rest, ".")
		var placeholder string
		if end < 0 {
			placeholder = rest
			rest = ""
		} else {
			placeholder = rest[:end]
			rest = rest[end+1:]
		}
		if placeholder == "" {
			out = append(out, ".")
			continue
		}
		if strings.Trim(placeholder, "#") == "" {
			if hadCounter {
				return nil, "", 0, errors.New("naming: multiple counters in pattern")
			}
			hadCounter = true
			counterWidth = len(placeholder)
			out = append(out, "\x00COUNTER")
			continue
		}
		v, err := renderPlaceholder(placeholder, now, resolve)
		if err != nil {
			return nil, "", 0, err
		}
		out = append(out, v)
		scope.WriteString(placeholder)
		scope.WriteString("=")
		scope.WriteString(v)
		scope.WriteString(";")
	}
	if scope.Len() == 0 {
		scope.WriteString("__")
	}
	return out, scope.String(), counterWidth, nil
}

func renderPlaceholder(p string, now time.Time, resolve Resolver) (string, error) {
	switch p {
	case "YYYY":
		return fmt.Sprintf("%04d", now.Year()), nil
	case "YY":
		return fmt.Sprintf("%02d", now.Year()%100), nil
	case "MM":
		return fmt.Sprintf("%02d", int(now.Month())), nil
	case "DD":
		return fmt.Sprintf("%02d", now.Day()), nil
	}
	if resolve != nil {
		if v, ok := resolve(p); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("naming: unknown placeholder %q", p)
}
