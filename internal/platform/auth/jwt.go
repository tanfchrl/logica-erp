package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Companies []string `json:"companies"`
	Roles     []string `json:"roles"`
	jwt.RegisteredClaims
}

type Signer struct {
	secret []byte
	ttl    time.Duration
	issuer string
}

func NewSigner(secret string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), ttl: ttl, issuer: "logica-erp"}
}

// Issue returns a signed JWT for the user with the given companies and roles.
func (s *Signer) Issue(userID string, companies, roles []string) (string, error) {
	now := time.Now().UTC()
	claims := Claims{
		Companies: companies,
		Roles:     roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
			NotBefore: jwt.NewNumericDate(now),
			ID:        randomID(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.secret)
}

// Verify parses and validates a JWT, returning the typed claims.
func (s *Signer) Verify(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		return s.secret, nil
	}, jwt.WithIssuer(s.issuer), jwt.WithLeeway(30*time.Second))
	if err != nil {
		return nil, err
	}
	c, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("auth: invalid token")
	}
	return c, nil
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rng.Read(b)
	return fmt.Sprintf("%x", b)
}
