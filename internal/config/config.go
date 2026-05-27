// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr  string
	Env       string // dev | prod
	LogLevel  string

	DatabaseURL string

	JWTSecret           string
	AccessTokenTTL      time.Duration
	RefreshTokenTTL     time.Duration
	RefreshCookieDomain string
	RefreshCookieSecure bool

	CORSOrigins []string

	StorageDriver   string // local | s3
	StorageLocalDir string
	S3Endpoint      string
	S3Region        string
	S3Bucket        string
	S3AccessKey     string
	S3SecretKey     string

	GotenbergURL string

	// Directory the agent contract registry scans for AGENT_CONTRACT.md files.
	// In dev this is the repo root; in production builds, contracts are
	// embedded into the binary so this is unused.
	AgentContractsDir string

	DefaultLocale   string
	DefaultTimeZone string
	DefaultCurrency string

	BootstrapAdminEmail    string
	BootstrapAdminPassword string
}

func Load() (Config, error) {
	c := Config{
		HTTPAddr:        env("LOGICA_HTTP_ADDR", ":8080"),
		Env:             env("LOGICA_ENV", "dev"),
		LogLevel:        env("LOGICA_LOG_LEVEL", "info"),
		DatabaseURL:     env("LOGICA_DATABASE_URL", ""),
		JWTSecret:       env("LOGICA_JWT_SECRET", ""),
		StorageDriver:   env("LOGICA_STORAGE_DRIVER", "local"),
		StorageLocalDir: env("LOGICA_STORAGE_LOCAL_DIR", "./data/uploads"),
		S3Endpoint:      env("LOGICA_S3_ENDPOINT", ""),
		S3Region:        env("LOGICA_S3_REGION", ""),
		S3Bucket:        env("LOGICA_S3_BUCKET", ""),
		S3AccessKey:     env("LOGICA_S3_ACCESS_KEY", ""),
		S3SecretKey:     env("LOGICA_S3_SECRET_KEY", ""),
		GotenbergURL:    env("LOGICA_GOTENBERG_URL", "http://localhost:3000"),
		AgentContractsDir: env("LOGICA_AGENT_CONTRACTS_DIR", "/src"),

		DefaultLocale:   env("LOGICA_DEFAULT_LOCALE", "id-ID"),
		DefaultTimeZone: env("LOGICA_DEFAULT_TIMEZONE", "Asia/Jakarta"),
		DefaultCurrency: env("LOGICA_DEFAULT_CURRENCY", "IDR"),

		RefreshCookieDomain:    env("LOGICA_REFRESH_COOKIE_DOMAIN", ""),
		BootstrapAdminEmail:    env("LOGICA_BOOTSTRAP_ADMIN_EMAIL", "admin@example.com"),
		BootstrapAdminPassword: env("LOGICA_BOOTSTRAP_ADMIN_PASSWORD", ""),
	}

	d, err := time.ParseDuration(env("LOGICA_ACCESS_TOKEN_TTL", "15m"))
	if err != nil {
		return c, fmt.Errorf("LOGICA_ACCESS_TOKEN_TTL: %w", err)
	}
	c.AccessTokenTTL = d
	d, err = time.ParseDuration(env("LOGICA_REFRESH_TOKEN_TTL", "720h"))
	if err != nil {
		return c, fmt.Errorf("LOGICA_REFRESH_TOKEN_TTL: %w", err)
	}
	c.RefreshTokenTTL = d
	c.RefreshCookieSecure = env("LOGICA_REFRESH_COOKIE_SECURE", "false") == "true"

	if origins := env("LOGICA_CORS_ORIGINS", ""); origins != "" {
		for _, o := range strings.Split(origins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				c.CORSOrigins = append(c.CORSOrigins, o)
			}
		}
	}

	if c.DatabaseURL == "" {
		return c, errors.New("LOGICA_DATABASE_URL is required")
	}
	if c.JWTSecret == "" || strings.HasPrefix(c.JWTSecret, "changeme") {
		if c.Env == "prod" {
			return c, errors.New("LOGICA_JWT_SECRET must be set in prod (generate: openssl rand -base64 64)")
		}
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
