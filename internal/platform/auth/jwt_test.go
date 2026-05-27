package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJWT_IssueAndVerify(t *testing.T) {
	s := NewSigner("test-secret-test-secret-test-secret-test-secret", 5*time.Minute)
	tok, err := s.Issue("usr_1", []string{"cmp_1", "cmp_2"}, []string{"system_administrator"})
	require.NoError(t, err)

	claims, err := s.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, "usr_1", claims.Subject)
	require.ElementsMatch(t, []string{"cmp_1", "cmp_2"}, claims.Companies)
	require.ElementsMatch(t, []string{"system_administrator"}, claims.Roles)
}

func TestJWT_RejectExpired(t *testing.T) {
	s := NewSigner("k", -time.Hour)
	tok, err := s.Issue("usr_1", nil, nil)
	require.NoError(t, err)

	_, err = s.Verify(tok)
	require.Error(t, err)
}

func TestJWT_RejectWrongSecret(t *testing.T) {
	a := NewSigner("secret-a", time.Minute)
	b := NewSigner("secret-b", time.Minute)

	tok, err := a.Issue("usr_1", nil, nil)
	require.NoError(t, err)
	_, err = b.Verify(tok)
	require.Error(t, err)
}
