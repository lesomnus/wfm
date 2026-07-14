# Wifi backend guide

This service controls wifi on a remote node over gRPC. Because the wifi
management stack differs per node (desktop = NetworkManager, Ubuntu
Server / edge = iwd, etc.), we put a **swappable backend** behind a single gRPC
interface.

## Architecture (the seam)

```
wifi.*ServiceServer (proto, 4 services)      ← external contract, immutable
  └ internal/server                          ← proto<->domain mapping, validation, status codes, Watch streaming (exactly one copy)
      └ internal/wnet  (Backend interface + domain types: Interface/AP/Profile/Active/Status)
          ├ wnet/nmcli   nmcli subprocess (legacy / zero-dependency fallback)
          ├ wnet/nmdbus  NetworkManager D-Bus (godbus, no fork)
          └ wnet/iwd     iwd D-Bus (godbus, no fork)
```

The seam is not the gRPC service but the **backend interface**
([internal/wnet/backend.go](../internal/wnet/backend.go)). The only thing that
changes per backend is "where the data comes from"; proto mapping, validation,
and the Watch loop are backend-independent, so that common logic lives in
`server` exactly once and a backend only provides data and actions in domain
terms. Capabilities a backend cannot express are reported as
`wnet.ErrUnsupported` / empty values, which `server` translates into
`codes.Unimplemented`.

id rule: Profile/Connection ids are always UUID strings. The NM family uses the
connection UUID assigned by NM directly; iwd has no native UUID, so it
deterministically derives `UUIDv5(ssid + "\0" + type)`
([internal/wnet/id.go](../internal/wnet/id.go)).

## Backend selection

```
wifi serve                     # autodetect (iwd → NM → nmcli order)
wifi serve --backend nmdbus    # explicit
wifi serve --backend iwd
wifi serve --backend nmcli
```

Autodetection picks iwd if `net.connman.iwd` is on the system bus, nmdbus if
`org.freedesktop.NetworkManager` is, and falls back to nmcli if neither is
([cmd/backend.go](../cmd/backend.go)).

> Mutations (profile add, connect, etc.) require root privileges (D-Bus polkit /
> nmcli). Run the server with `sudo`.

## Backend comparison

| Aspect | nmcli | nmdbus | iwd |
|---|---|---|---|
| Transport | nmcli subprocess (fork per call) | NM D-Bus (godbus, no fork) | iwd D-Bus (godbus, no fork) |
| Watch | 400ms polling | StateChanged signal | PropertiesChanged signal |
| Profile store | NM connection | NM connection | `/var/lib/iwd/<ssid>.<type>` files |
| scan BSSID/frequency | provided | provided | **not provided** (abstracted per SSID) |
| Pin BSSID on connect | supported | supported | **not supported** |
| Static IP | supported | supported | supported (file `[IPv4]`) |
| Failure cause detail | auth/unknown | auth(NO_SECRETS)/unknown | **generic** (Connect error) |
| enterprise (802.1X) | not implemented | not implemented | not implemented |

For edge / low-latency environments, prefer the fork-free **nmdbus** (NM nodes)
or **iwd** (Ubuntu Server) backends. nmcli is kept as a fallback that works
without any D-Bus code.

### Common limitations (all backends)

- `AccessPoint.signal` / `ConnectionStatus.signal` are a **quality of 0-100**
  (the same scale as NM Strength), not dBm (the proto comments were corrected to
  say so).
- `Profile.desc` / `Profile.date_created` are **not stored or returned**.
  Neither NM nor iwd has a natural place to store an arbitrary
  description/creation time, so this metadata — which is outside the core
  features (scan/connect/IP) — is intentionally not handled. Setting it is
  ignored.
- enterprise (802.1X) is unimplemented in all three backends (`Unimplemented` on
  `Add`/`Patch`).

### iwd backend limitations (gaps vs proto)

- scan results have **no BSSID/frequency** (iwd abstracts BSS/roaming).
  `AccessPoint.bssid` is empty, and so is `id`.
- A connection **cannot be pinned to a specific BSSID** (`Connection.Add`'s
  access_point is ignored).
- Profile `name` is effectively the SSID (an iwd known-network is identified by
  SSID+type). One profile per SSID.
- Connection failures come back as a synchronous error from
  `Network.Connect()`, so `Connection.Add` returns the error immediately. It
  does not distinguish auth failures from generic failures.
- enterprise (802.1X) is unimplemented (`ErrUnsupported`).

---

# iwd configuration (Ubuntu Server / edge)

To use the iwd backend, the node must have iwd installed and running, and iwd
must own that wlan. The default Ubuntu Server stack is not iwd but
**wpa_supplicant (connectivity) + systemd-networkd (DHCP)**, so switching to iwd
requires the configuration below.

## 1. Install

```bash
sudo apt install iwd
sudo systemctl enable --now iwd
```

## 2. Daemon config `/etc/iwd/main.conf`

This must be enabled for iwd to configure **IP as well** (DHCP or static)
directly. The default is `false`; without it iwd does only L2
(connectivity/auth) and IP is left to something external (networkd, etc.).

```ini
[General]
EnableNetworkConfiguration=true

[Network]
NameResolvingService=systemd   # use systemd-resolved. For openresolv, use resolvconf
EnableIPv6=true
```

After editing, `sudo systemctl restart iwd`.

## 3. Profile (known network) file `/var/lib/iwd/<ssid>.<type>`

- `<type>` = `open` | `psk` | `8021x`
- **Filename encoding**: if the SSID consists only of
  alphanumerics/space/`_`/`-`, use it as-is; otherwise use `=` + the lowercase
  hex of the original bytes. e.g. `My WiFi` (psk) → `My WiFi.psk`,
  `Café` (psk) → `=436166c3a9.psk`.
- **Permissions**: directory `0700 root:root`; files are `0600 root:root` since
  the password is plaintext.
- iwd **watches** this directory, so writing a file correctly makes it
  recognized immediately without a restart (= profile creation). Deleting it
  forgets the network.

This backend's `AddProfile`/`PatchProfile`/`DeleteProfile` write/delete exactly
these files ([internal/wnet/iwd/profile.go](../internal/wnet/iwd/profile.go)).

### (a) WPA2-PSK + DHCP

`/var/lib/iwd/HomeNet.psk`:

```ini
[Security]
Passphrase=correcthorsebattery

[Settings]
AutoConnect=true
```

Without an `[IPv4]` section, iwd's built-in DHCP runs when
`EnableNetworkConfiguration=true`.

### (b) WPA2-PSK + static IPv4

`/var/lib/iwd/HomeNet.psk`:

```ini
[Security]
Passphrase=correcthorsebattery

[Settings]
AutoConnect=true

[IPv4]
Address=192.168.1.50          # not CIDR! the prefix goes in Netmask
Netmask=255.255.255.0
Gateway=192.168.1.1
DNS=192.168.1.1 1.1.1.1       # space-separated
DomainName=home.lan
```

> Note: for IPv4, `Address` is a plain IP and the prefix is a separate `Netmask`
> key. IPv6 is the opposite — use CIDR, e.g. `[IPv6] Address=2001:db8::50/64`.

### Security key options

- To use a precomputed PSK instead of `Passphrase`, use
  `PreSharedKey=<64 hex chars>` (avoids plaintext).
- `8021x` (enterprise) uses `EAP-Method`, `EAP-Identity`, etc. — unsupported by
  this backend.

## 4. Coexisting with NM: only a specific wlan owned by iwd

If the node also runs NM (e.g. keep the lifeline wlan on NM and only put the
test wlan on iwd), the two daemons must not fight over the same device.

```bash
# Keep NM from managing the test wlan (e.g. husr)
sudo tee /etc/NetworkManager/conf.d/unmanage-husr.conf <<'CONF'
[keyfile]
unmanaged-devices=interface-name:husr
CONF
sudo systemctl reload NetworkManager
# iwd then grabs husr. The lifeline wlan stays managed by NM.
```

> iwd has no per-device allowlist, so on startup it tries to grab every
> available wlan. So for a wlan NM should keep, don't explicitly unmanage it on
> the NM side — leave NM holding it, and remove only the wlan to hand to iwd from
> NM. If there is a single wlan and you'll use that node exclusively with iwd,
> turn off the NM/wpa_supplicant path
> (`sudo systemctl disable --now NetworkManager wpa_supplicant`).

## 5. Note on the default Ubuntu Server stack (no iwd)

Without iwd, on a stock Ubuntu Server, wifi runs via the wpa_supplicant +
systemd-networkd combination that netplan generates. In that case this service's
iwd backend cannot be used (different daemon), and nmdbus is also impossible
without NM. To support such a node, choose either (1) install iwd and apply the
config above, or (2) install NM (`apt install network-manager`, set the netplan
renderer to NetworkManager) to use nmdbus/nmcli.

---

# Development notes

- D-Bus is called directly via `github.com/godbus/dbus/v5` (pure Go, no
  libnm/cgo/glib).
- Pure logic (filename encoding, profile render/parse, flags→keymgmt, signal
  conversion, id derivation) is covered by unit tests
  (`go test ./internal/...`).
- Real-hardware verification: the NM backends (nmcli/nmdbus) can be checked with
  a scan on a spare wlan of the test node. The iwd backend is verified after
  installing iwd on the node and doing the handover in section 4 (be careful not
  to touch the lifeline wlan).
