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

## Backends

| Backend | Target | Notes |
|---|---|---|
| `nmdbus` | NetworkManager nodes | NM D-Bus directly (no fork), signal-based Watch |
| `iwd` | Ubuntu Server / edge | iwd D-Bus directly (no fork), profiles = files under `/var/lib/iwd` |
| `nmcli` | NM nodes (fallback) | nmcli subprocess, no D-Bus code required |

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
