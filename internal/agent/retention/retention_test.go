package retention

import (
	"os"
	"testing"
)

func TestEnvInt(t *testing.T) {
	const name = "TEST_RETENTION_ENVINT"
	t.Cleanup(func() { _ = os.Unsetenv(name) })

	t.Run("unset returns default", func(t *testing.T) {
		_ = os.Unsetenv(name)
		if got := envInt(name, 30); got != 30 {
			t.Errorf("envInt unset = %d, want 30", got)
		}
	})
	t.Run("valid positive int", func(t *testing.T) {
		_ = os.Setenv(name, "7")
		if got := envInt(name, 30); got != 7 {
			t.Errorf("envInt = %d, want 7", got)
		}
	})
	t.Run("non-numeric falls back", func(t *testing.T) {
		_ = os.Setenv(name, "not-a-number")
		if got := envInt(name, 30); got != 30 {
			t.Errorf("envInt non-numeric = %d, want 30 (fallback)", got)
		}
	})
	t.Run("zero is rejected", func(t *testing.T) {
		_ = os.Setenv(name, "0")
		if got := envInt(name, 30); got != 30 {
			t.Errorf("envInt zero = %d, want 30 (fallback — zero is not a positive retention)", got)
		}
	})
	t.Run("negative is rejected", func(t *testing.T) {
		_ = os.Setenv(name, "-1")
		if got := envInt(name, 30); got != 30 {
			t.Errorf("envInt negative = %d, want 30 (fallback)", got)
		}
	})
}
