package dbx

import (
	"crypto/rand"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// NewID returns a ULID-as-text suitable for use as a primary key.
// Prefixed by the doctype short code for log-friendliness, e.g. "cmp_01H...".
// The prefix is not part of the ULID itself; it is appended only when callers ask for it.
func NewID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// NewIDWithPrefix returns "<prefix>_<ulid>". The prefix is lowercased and stripped of underscores.
func NewIDWithPrefix(prefix string) string {
	p := strings.ToLower(strings.ReplaceAll(prefix, "_", ""))
	return p + "_" + NewID()
}
