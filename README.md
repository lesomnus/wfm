# wfm

A gRPC service and CLI for controlling wifi on a remote node. It exposes the
same interface — **scan APs → create a profile → connect → query status** —
regardless of the node's wifi management stack (NetworkManager / iwd).

Services: `Interface` (wireless devices), `AccessPoint` (scan), `Profile`
(CRUD over saved settings), `Connection` (active connections + Watch/Status).
Definitions live in [proto/wifi](proto/wifi).

Server and client build into a single `wfm` binary (root `main.go`); the
commands live in the [`cmd`](cmd) package. Config / OpenTelemetry / version
scaffolding is in [`cmd/config`](cmd/config) and [`cmd/version`](cmd/version).

## Build

```bash
go build ./...
go test ./...
```

## Run

```bash
# Server (backend autodetected: iwd → NM → nmcli). Mutations require root.
sudo go run . serve                 # or --backend nmdbus|iwd|nmcli
                                    # --addr :50051

# Client (in another shell / on another node)
go run . interface list
go run . scan <iface>
go run . connect <iface> <ssid> [psk]
go run . profile list
go run . connection watch <uuid>
```

Use `--server <host:port>` to point at a remote server (default
`127.0.0.1:50051`).

Global options:

- `--config <path>` — config file path (default lookup: `wfm.yaml`, `wfm.yml`).
- `wfm config` — print the current config as YAML.
- `wfm version` — print version information.

## Configuration

### Excluding interfaces

The server can hide interfaces from wfm entirely. An excluded interface never
appears in `interface list`, and every operation targeting it — get, set-power,
scan, connect, and any connection bound to it — behaves as if the interface did
not exist. The exclusion is enforced in the backend the server serves, so it
applies to every client and to the in-process server a bare CLI invocation
starts; there is no way to reach an excluded interface through wfm.

An interface can be excluded by name, MAC address, or the PCI address of its
backing device:

```yaml
interface:
  exclude:
    - name: eth0                 # exact interface name
    - mac: "AA:BB:CC:DD:EE:FF"   # case- and separator-insensitive
    - pci: "0000:02:00.0"        # "02:00.0" (no domain) matches too
```

Each list entry is one rule. A rule may combine fields — e.g. `{name: wlan0,
mac: ...}` matches only an interface satisfying **all** of them — and an
interface is excluded if **any** rule matches it. Matching runs on the neutral
attributes the backend reports (name, MAC, PCI), so exclusion is stable across
interface renames and works the same for a local or a remote backend. A backend
that cannot determine an attribute (e.g. `ubus` has no PCI address) simply never
matches a rule pinning it, so use name/MAC there.

### OpenWrt (ubus) backend

The `ubus` backend controls wifi on a **remote OpenWrt node** over ubus exposed
as JSON-RPC by `rpcd` + `uhttpd` (the `uhttpd-mod-ubus` package), reached at an
HTTP(S) endpoint like `http://<node>/ubus`. Unlike the local backends it talks
to a different host, so the wfm server can run anywhere. Select it explicitly —
it is never autodetected — and configure it:

```yaml
ubus:
  endpoint: http://192.168.1.1/ubus
  username: root
  password_file: /run/secrets/wfm-ubus   # preferred over inline `password`
  # insecure: true      # skip TLS verification for a self-signed uhttpd cert
  # radio: radio0        # wifi-device new station profiles attach to
  # network: wwan        # network interface a station binds to for DHCP
```

```bash
wfm serve --backend ubus                 # server talking to the node in the config
# or self-serving, in one shot:
wfm --backend ubus interface list
```

The node needs `rpcd`/`uhttpd-mod-ubus` installed and an rpcd login whose ACL
grants the `iwinfo`, `uci` (wireless) and `network.wireless`/`network.interface`
objects. The secret is read from `password_file` so it never appears in
`wfm config` output. OpenWrt is AP-centric; wfm's scan/connect model applies to
a `wifi-iface` in **station** mode, and capabilities OpenWrt cannot express
here — enterprise security, per-profile static IP, per-interface radio power —
are reported as `Unimplemented`.

## Backends

| Backend | Target | Notes |
|---|---|---|
| `nmdbus` | NetworkManager nodes | NM D-Bus directly (no fork), signal-based Watch |
| `iwd` | Ubuntu Server / edge | iwd D-Bus directly (no fork), profiles = files under `/var/lib/iwd` |
| `nmcli` | NM nodes (fallback) | nmcli subprocess, no D-Bus code required |
| `ubus` | OpenWrt (remote) | ubus over HTTP JSON-RPC; profiles = `wifi-iface` (sta) in UCI; poll-based Watch |

For backend architecture, a capability/limitation comparison, and the **full
iwd configuration (main.conf and profile files)**, see
[docs/backends.md](docs/backends.md).

## Regenerating proto code

The proto definitions live in [`proto/wifi`](proto/wifi) and the generated Go
bindings in [`internal/wifi`](internal/wifi). To regenerate:

```bash
npm install     # install buf plugins/tools (first time only)
./gen.mts       # buf generate + merge service protos
```
