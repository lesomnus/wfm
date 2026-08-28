# OpenWrt ubus live-test harness

Stands up a **self-contained OpenWrt node in QEMU** — two `mac80211_hwsim`
virtual radios (one AP, one free for wfm to manage as a station) — so the `ubus`
backend can be exercised against a *real* OpenWrt stack (real `rpcd`, `netifd`,
`hostapd`/`wpa_supplicant`, `iwinfo`, `uci`) instead of the in-repo fake.

The virtual radios live entirely inside the guest kernel, so **nothing on the
host kernel is touched** — unlike loading `mac80211_hwsim` on the host for a
container, which would leak virtual radios into every other project sharing the
engine.

## Layout

| Path | Role |
|---|---|
| `files/etc/uci-defaults/99-wfm-test` | baked into the image; self-provisions the node on first boot (AP `WFMTEST`, `wwan` DHCP client, ubus at `/ubus`, root password) |
| `build-image.sh` | builds the image via OpenWrt ImageBuilder (needs internet once) |
| `run-vm.sh` | boots the image under QEMU (KVM if available, else TCG) and waits for ubus |
| `acl/wfm.json` | example **scoped** rpcd ACL for production (the test uses root) |

The matching Go test is `TestUbusLive` in `internal/wnet/ubus/live_test.go`,
gated on `WFM_TEST_UBUS` so `go test ./...` skips it with no infrastructure.

## Run it locally

```bash
# 1. Build the image (once; needs internet for the ImageBuilder).
bash test/openwrt/build-image.sh            # -> test/openwrt/openwrt.img

# 2. Boot the VM and wait until ubus answers.
bash test/openwrt/run-vm.sh start

# 3. Run the live test against it.
WFM_TEST_UBUS=http://127.0.0.1:8080/ubus \
WFM_TEST_UBUS_USER=root WFM_TEST_UBUS_PASS=wfmtest \
WFM_TEST_UBUS_SSID=WFMTEST WFM_TEST_UBUS_PSK=wfmtest123 \
WFM_TEST_UBUS_RADIO=radio1 WFM_TEST_UBUS_CONNECT=1 \
go test -run TestUbusLive -v -count=1 ./internal/wnet/ubus/

# 4. Tear down.
bash test/openwrt/run-vm.sh stop
```

You can also drive the VM with the CLI directly:

```bash
printf 'ubus:\n  endpoint: http://127.0.0.1:8080/ubus\n  username: root\n  password: wfmtest\n  radio: radio1\n' > /tmp/wfm-owrt.yaml
go run . --config /tmp/wfm-owrt.yaml --backend ubus interface list
go run . --config /tmp/wfm-owrt.yaml --backend ubus scan radio1   # station device name from `interface list`
```

## In CI

`.github/workflows/ubus-live.yml` does the same on `ubuntu-latest` (which has
internet egress and, on current runners, `/dev/kvm`): install QEMU, cache/build
the image, boot, run `TestUbusLive`. It is `workflow_dispatch` plus changes to
the backend/harness, so it does not run on every push.

## Status / caveats

This harness is **scaffolding to be validated on its first real run** — the
OpenWrt-specific bits (device/section names, `network.wireless up` as the apply
step, the `mgmt`/`lan`/`wwan` split, non-interactive `passwd`) are best-effort
and are exactly the assumptions the live test exists to confirm. Expect to
iterate on `files/etc/uci-defaults/99-wfm-test` after the first boot; the VM's
serial log is written to `test/openwrt/.vm.serial.log`, and SSH is forwarded to
`localhost:2222` for debugging. Once a run passes, capture the real ubus JSON it
produced and freeze it as fixtures to replace the fake responses in
`ubus_test.go`.
