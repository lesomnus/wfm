#!/usr/bin/env bash
# Build a self-provisioning OpenWrt x86-64 image for the wfm ubus live test,
# using the official ImageBuilder so the result is reproducible and offline: the
# needed packages and the files/ overlay (which self-provisions on first boot)
# are baked in, so the VM needs no internet at runtime.
#
# Requires internet access to fetch the ImageBuilder (run it in CI or anywhere
# egress is available), plus: make, tar, xz, gzip, curl.
#
# Usage: test/openwrt/build-image.sh [output-image-path]
set -euo pipefail

VER="${OPENWRT_VERSION:-23.05.5}"
OUT="${1:-test/openwrt/openwrt.img}"

# wpad (full) provides hostapd + wpa_supplicant for both AP and station PSK.
# The ubus objects wfm calls come from: rpcd-mod-iwinfo (the `iwinfo` object),
# rpcd core (`uci`, `session`), netifd (`network.wireless`/`network.interface`,
# in base), and uhttpd-mod-ubus (the HTTP `/ubus` endpoint). kmod-mac80211-hwsim
# gives the virtual radios; dnsmasq the DHCP/DNS.
PKGS="${OPENWRT_PACKAGES:-kmod-mac80211-hwsim wpad-mbedtls iwinfo rpcd rpcd-mod-iwinfo uhttpd uhttpd-mod-ubus dnsmasq}"

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

base="openwrt-imagebuilder-${VER}-x86-64.Linux-x86_64"
url="https://downloads.openwrt.org/releases/${VER}/targets/x86/64/${base}.tar.xz"

echo "==> fetching ImageBuilder ${VER}"
curl -fSL "$url" -o "$work/ib.tar.xz"
tar -C "$work" -xf "$work/ib.tar.xz"
ib="$work/$base"

echo "==> building image (packages: $PKGS)"
make -C "$ib" image \
	PROFILE="generic" \
	PACKAGES="$PKGS" \
	FILES="$here/files"

# The squashfs combined image auto-expands its overlay to fill the disk on first
# boot, leaving room for runtime state.
img_gz="$(ls "$ib"/bin/targets/x86/64/*squashfs-combined.img.gz | head -1)"
mkdir -p "$(dirname "$root/$OUT")"

# OpenWrt pads its .img.gz after the gzip stream, so gzip decompresses fine but
# exits 2 ("trailing garbage ignored"). That warning is not an error; only a
# non-zero code other than 2 is a real failure.
set +e
gunzip -c "$img_gz" > "$root/$OUT"
rc=$?
set -e
if [ "$rc" -ne 0 ] && [ "$rc" -ne 2 ]; then
	echo "gunzip failed ($rc)" >&2
	exit "$rc"
fi
echo "==> wrote $OUT"
