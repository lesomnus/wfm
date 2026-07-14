package wnet

import (
	"testing"

	"github.com/google/uuid"
)

func TestDeriveID(t *testing.T) {
	a := DeriveID("HomeNet", "psk")
	if _, err := uuid.Parse(a); err != nil {
		t.Fatalf("DeriveID did not produce a valid uuid: %q (%v)", a, err)
	}
	if b := DeriveID("HomeNet", "psk"); a != b {
		t.Errorf("DeriveID not deterministic: %q != %q", a, b)
	}
	if c := DeriveID("HomeNet", "open"); a == c {
		t.Errorf("DeriveID collided across types: %q", a)
	}
	if d := DeriveID("Other", "psk"); a == d {
		t.Errorf("DeriveID collided across ssids: %q", a)
	}
}
