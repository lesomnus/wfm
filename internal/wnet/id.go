package wnet

import (
	"strings"

	"github.com/google/uuid"
)

// idNamespace is a fixed namespace for deriving stable profile/connection ids
// on backends that have no native UUID of their own (e.g. iwd, whose known
// networks are keyed by SSID + security type). Using a deterministic UUIDv5
// keeps the proto's 16-byte id working and lets a backend round-trip an id back
// to its native object by recomputing it for each candidate.
var idNamespace = uuid.MustParse("a3b1f2c4-5d6e-4f70-8a9b-0c1d2e3f4a5b")

// DeriveID returns a deterministic UUID string for the given key parts.
func DeriveID(parts ...string) string {
	return uuid.NewSHA1(idNamespace, []byte(strings.Join(parts, "\x00"))).String()
}
