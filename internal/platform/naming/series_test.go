package naming

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCompile_BasicCounter(t *testing.T) {
	pat := "INV-.YYYY.-.####"
	parts, scope, width, err := compile(pat, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), nil)
	require.NoError(t, err)
	require.Equal(t, 4, width)
	require.Contains(t, scope, "YYYY=2026")
	require.Equal(t, []string{"INV-", "2026", "-", "\x00COUNTER"}, parts)
}

func TestCompile_ResolverLookup(t *testing.T) {
	pat := "PI-.company_abbr.-.YYYY.-.####"
	parts, scope, width, err := compile(pat, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		func(p string) (string, bool) {
			if p == "company_abbr" {
				return "DEMO", true
			}
			return "", false
		})
	require.NoError(t, err)
	require.Equal(t, 4, width)
	require.Contains(t, scope, "company_abbr=DEMO")
	require.Equal(t, "DEMO", parts[1])
}

func TestCompile_DateParts(t *testing.T) {
	pat := ".YYYY..MM..DD.-.####"
	parts, _, _, err := compile(pat, time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), nil)
	require.NoError(t, err)
	require.Equal(t, "2026", parts[0])
	require.Equal(t, "03", parts[1])
	require.Equal(t, "07", parts[2])
}

func TestCompile_RejectsMultipleCounters(t *testing.T) {
	// Two dot-bounded counters: ".####." appears twice.
	_, _, _, err := compile("X-.####.-.####.", time.Now(), nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "multiple"))
}

func TestCompile_UnknownPlaceholder(t *testing.T) {
	_, _, _, err := compile("X-.unknown.-.####", time.Now(), nil)
	require.Error(t, err)
}
