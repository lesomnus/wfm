# ubus backend test fixtures

Each file is the **data payload** of one ubus call — the second element of the
`[status, data]` result — for a shape that the backend parses. `fixture_test.go`
decodes them with the exact structs the backend uses and asserts the parsers
extract the right domain values.

| Fixture | ubus call | parsed by |
|---|---|---|
| `iwinfo_devices.json` | `iwinfo devices` | `Interfaces` |
| `iwinfo_info.json` | `iwinfo info {device}` | `toIface` |
| `iwinfo_scan.json` | `iwinfo scan {device}` | `apFromScan` |
| `uci_get_wireless.json` | `uci get {config:"wireless"}` | `parseWifiIfaces` / `toProfile` |
| `network_wireless_status.json` | `network.wireless status` | `parseWirelessStatus` |
| `network_interface_status.json` | `network.interface.<n> status` | `parseIfStatus` |

## ⚠️ These are synthetic placeholders

They were hand-written to the **best-effort** shape of real OpenWrt output, not
captured from a device. They make the parser tests pass and encode the current
field-name assumptions (e.g. `mhz` in scan vs `frequency` in info, the
`encryption` block, `interfaces[].section`/`ifname`). Until they are replaced
with real capture they add **no independent fidelity** — they agree with the
code because the same person wrote both.

The value is the mechanism: the assertions are semantic (an AP named `HomeNet`
at ~64 % on 5180 MHz, a PSK station profile, a DHCP lease), so when a fixture is
replaced with **real** JSON whose shape differs, the decode yields zero values
and the test fails — pinpointing exactly which assumption was wrong.

## Regenerating from a real node

Run the live test against a real (or QEMU, see `test/openwrt/`) OpenWrt node with
`WFM_TEST_UBUS_CAPTURE` pointing here; the capturing transport
(`capture_test.go`) overwrites these files with the node's actual responses:

```bash
WFM_TEST_UBUS=http://127.0.0.1:8080/ubus \
WFM_TEST_UBUS_USER=root WFM_TEST_UBUS_PASS=wfmtest \
WFM_TEST_UBUS_RADIO=radio1 WFM_TEST_UBUS_CONNECT=1 \
WFM_TEST_UBUS_CAPTURE=$PWD/internal/wnet/ubus/testdata \
go test -run TestUbusLive -count=1 ./internal/wnet/ubus/
```

Then rerun `go test ./internal/wnet/ubus/` (without the env) and fix any parser
or assertion that the real shapes broke. `iwinfo_info.json` and
`network_interface_status.json` capture whichever device/interface the run
touched last; review them before committing.
