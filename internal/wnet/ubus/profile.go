package ubus

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"

	"github.com/lesomnus/wfm/internal/wnet"
)

// sectionName is the deterministic wifi-iface section name wfm uses for a
// station profile, so re-adding the same (ssid, security) reuses one section
// instead of accumulating duplicates. It is independent of the profile id.
func sectionName(ssid string, k wnet.SecurityKind) string {
	sum := sha1.Sum([]byte(ssid + "\x00" + secType(k)))
	return "wfm_" + hex.EncodeToString(sum[:6])
}

// findByID locates the station section whose (ssid, security) derives to id.
func (b *Backend) findByID(ctx context.Context, id string) (wifiIface, bool, error) {
	stas, err := b.staIfaces(ctx)
	if err != nil {
		return wifiIface{}, false, err
	}
	for _, w := range stas {
		if profileID(w.SSID, encStrToSecKind(w.Encryption)) == id {
			return w, true, nil
		}
	}
	return wifiIface{}, false, nil
}

// findByProfile locates the station section for a given ssid + security kind.
func (b *Backend) findByProfile(ctx context.Context, ssid string, k wnet.SecurityKind) (wifiIface, bool, error) {
	return b.findByID(ctx, profileID(ssid, k))
}

func (b *Backend) Profiles(ctx context.Context) ([]wnet.Profile, error) {
	stas, err := b.staIfaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wnet.Profile, 0, len(stas))
	for _, w := range stas {
		out = append(out, toProfile(w))
	}
	return out, nil
}

func (b *Backend) Profile(ctx context.Context, id string) (wnet.Profile, error) {
	w, ok, err := b.findByID(ctx, id)
	if err != nil {
		return wnet.Profile{}, err
	}
	if !ok {
		return wnet.Profile{}, fmt.Errorf("%w: profile %s", wnet.ErrNotFound, id)
	}
	return toProfile(w), nil
}

// rejectUnsupported guards profile writes against capabilities OpenWrt/wfm does
// not express here: enterprise security and per-profile static IP.
func rejectUnsupported(sec wnet.Security, ipv4, ipv6 *wnet.IPConfig) error {
	if sec.Kind == wnet.SecEnterprise {
		return fmt.Errorf("%w: enterprise security", wnet.ErrUnsupported)
	}
	if manualIP(ipv4) || manualIP(ipv6) {
		return fmt.Errorf("%w: per-profile static IP (configure it in the node's network config)", wnet.ErrUnsupported)
	}
	return nil
}

func manualIP(c *wnet.IPConfig) bool {
	return c != nil && c.Method == wnet.IPManual
}

// writeStation creates or updates the station wifi-iface section for a profile
// and commits. When key is empty for a PSK profile the stored key is preserved.
func (b *Backend) writeStation(ctx context.Context, ssid string, sec wnet.Security, autoconnect bool, existing *wifiIface) error {
	values := map[string]any{
		"mode":       "sta",
		"ssid":       ssid,
		"encryption": secKindToEnc(sec.Kind),
		"disabled":   boolToUCI(!autoconnect),
	}
	if b.network != "" {
		values["network"] = b.network
	}
	switch sec.Kind {
	case wnet.SecPSK:
		if sec.Passphrase != "" {
			values["key"] = sec.Passphrase
		} else if existing != nil && existing.Key != "" {
			values["key"] = existing.Key // preserve secret on a keyless patch
		}
	case wnet.SecOpen:
		values["key"] = "" // clear any stored key
	}

	if existing != nil {
		return b.setAndCommit(ctx, existing.Section, values)
	}

	radio, err := b.pickRadio(ctx)
	if err != nil {
		return err
	}
	values["device"] = radio
	name := sectionName(ssid, sec.Kind)
	if err := b.c.Call(ctx, "uci", "add", map[string]any{
		"config": "wireless",
		"type":   "wifi-iface",
		"name":   name,
		"values": values,
	}, nil); err != nil {
		return err
	}
	return b.commit(ctx)
}

func (b *Backend) setAndCommit(ctx context.Context, section string, values map[string]any) error {
	if err := b.c.Call(ctx, "uci", "set", map[string]any{
		"config":  "wireless",
		"section": section,
		"values":  values,
	}, nil); err != nil {
		return err
	}
	return b.commit(ctx)
}

func (b *Backend) AddProfile(ctx context.Context, spec wnet.ProfileSpec) (wnet.Profile, error) {
	if spec.SSID == "" {
		return wnet.Profile{}, fmt.Errorf("ssid is required")
	}
	if err := rejectUnsupported(spec.Security, spec.IPv4, spec.IPv6); err != nil {
		return wnet.Profile{}, err
	}
	existing, ok, err := b.findByProfile(ctx, spec.SSID, spec.Security.Kind)
	if err != nil {
		return wnet.Profile{}, err
	}
	var cur *wifiIface
	if ok {
		cur = &existing
	}
	if err := b.writeStation(ctx, spec.SSID, spec.Security, spec.Autoconnect, cur); err != nil {
		return wnet.Profile{}, err
	}
	return b.Profile(ctx, profileID(spec.SSID, spec.Security.Kind))
}

func (b *Backend) PatchProfile(ctx context.Context, id string, patch wnet.ProfilePatch) (wnet.Profile, error) {
	cur, ok, err := b.findByID(ctx, id)
	if err != nil {
		return wnet.Profile{}, err
	}
	if !ok {
		return wnet.Profile{}, fmt.Errorf("%w: profile %s", wnet.ErrNotFound, id)
	}

	ssid := cur.SSID
	if patch.SSID != nil {
		ssid = *patch.SSID
	}
	sec := wnet.Security{Kind: encStrToSecKind(cur.Encryption)}
	if patch.Security != nil {
		sec = *patch.Security
	}
	autoconnect := !cur.Disabled
	if patch.Autoconnect != nil {
		autoconnect = *patch.Autoconnect
	}
	if err := rejectUnsupported(sec, patch.IPv4, patch.IPv6); err != nil {
		return wnet.Profile{}, err
	}

	// If ssid or security kind changed the section identity changes; recreate
	// under the new deterministic name and drop the old section.
	if ssid != cur.SSID || sec.Kind != encStrToSecKind(cur.Encryption) {
		if sec.Kind == wnet.SecPSK && sec.Passphrase == "" {
			sec.Passphrase = cur.Key // carry the secret across a rename
		}
		if err := b.writeStation(ctx, ssid, sec, autoconnect, nil); err != nil {
			return wnet.Profile{}, err
		}
		if err := b.deleteSection(ctx, cur.Section); err != nil {
			return wnet.Profile{}, err
		}
	} else {
		if err := b.writeStation(ctx, ssid, sec, autoconnect, &cur); err != nil {
			return wnet.Profile{}, err
		}
	}
	return b.Profile(ctx, profileID(ssid, sec.Kind))
}

func (b *Backend) DeleteProfile(ctx context.Context, id string) error {
	cur, ok, err := b.findByID(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: profile %s", wnet.ErrNotFound, id)
	}
	return b.deleteSection(ctx, cur.Section)
}

func (b *Backend) deleteSection(ctx context.Context, section string) error {
	if err := b.c.Call(ctx, "uci", "delete", map[string]any{
		"config":  "wireless",
		"section": section,
	}, nil); err != nil {
		return err
	}
	return b.commit(ctx)
}
