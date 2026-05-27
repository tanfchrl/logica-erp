package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPassword_Roundtrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	require.NoError(t, err)
	require.NotEmpty(t, hash)

	ok, err := VerifyPassword("correct-horse-battery-staple", hash)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = VerifyPassword("wrong-password", hash)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPassword_RejectEmpty(t *testing.T) {
	_, err := HashPassword("")
	require.Error(t, err)
}

func TestPassword_UniqueSalt(t *testing.T) {
	h1, err := HashPassword("same-password")
	require.NoError(t, err)
	h2, err := HashPassword("same-password")
	require.NoError(t, err)
	require.NotEqual(t, h1, h2, "same plaintext must produce different hashes")
}
