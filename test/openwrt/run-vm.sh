#!/usr/bin/env bash
# Boot the self-provisioning OpenWrt image under QEMU and wait until its ubus
# endpoint answers, then leave it running for the live test. Uses KVM when
# /dev/kvm is writable, else falls back to software emulation (slower).
#
# Usage:
#   test/openwrt/run-vm.sh start   # boot + wait for ubus (default)
#   test/openwrt/run-vm.sh wait    # just wait for ubus
#   test/openwrt/run-vm.sh stop    # kill the VM and clean up
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
IMG="${WFM_OPENWRT_IMG:-$here/openwrt.img}"
UBUS_PORT="${WFM_UBUS_PORT:-8080}"
SSH_PORT="${WFM_SSH_PORT:-2222}"
MEM="${WFM_VM_MEM:-256}"
DISK="${WFM_VM_DISK:-512M}"
PW="${WFM_TEST_UBUS_PASS:-wfmtest}"

runimg="$here/.vm.img"
pidf="$here/.vm.pid"
log="$here/.vm.serial.log"

wait_ubus() {
	echo "==> waiting for ubus at http://127.0.0.1:$UBUS_PORT/ubus"
	local req out
	req='{"jsonrpc":"2.0","id":1,"method":"call","params":["00000000000000000000000000000000","session","login",{"username":"root","password":"'"$PW"'"}]}'
	for _ in $(seq 1 150); do
		out="$(curl -sS -m 2 "http://127.0.0.1:$UBUS_PORT/ubus" -d "$req" 2>&1)" || true
		if printf '%s' "$out" | grep -q ubus_rpc_session; then
			echo "==> ubus is up"
			return 0
		fi
		sleep 2
	done
	{
		echo "==> timed out waiting for ubus"
		echo "--- last curl response (empty/refused = network/firewall; a JSON error = login/ACL) ---"
		printf '%s\n' "$out"
		echo "--- full serial log ---"
		cat "$log" 2>/dev/null || true
	} >&2
	return 1
}

start() {
	command -v qemu-system-x86_64 >/dev/null || { echo "qemu-system-x86_64 not found" >&2; exit 1; }
	[ -f "$IMG" ] || { echo "image not found: $IMG (run build-image.sh)" >&2; exit 1; }

	cp -f "$IMG" "$runimg"
	qemu-img resize -f raw "$runimg" "$DISK" >/dev/null

	local accel="tcg"
	if [ -w /dev/kvm ]; then accel="kvm"; fi
	echo "==> booting OpenWrt VM (accel=$accel)"

	qemu-system-x86_64 \
		-machine "q35,accel=$accel" -m "$MEM" -smp 2 \
		-drive file="$runimg",format=raw,if=virtio \
		-netdev "user,id=n0,hostfwd=tcp::${UBUS_PORT}-:80,hostfwd=tcp::${SSH_PORT}-:22" \
		-device virtio-net,netdev=n0 \
		-display none -serial "file:$log" \
		-daemonize -pidfile "$pidf"

	wait_ubus
}

stop() {
	if [ -f "$pidf" ]; then
		kill "$(cat "$pidf")" 2>/dev/null || true
		rm -f "$pidf"
	fi
	rm -f "$runimg"
	echo "==> VM stopped"
}

case "${1:-start}" in
	start) start ;;
	wait)  wait_ubus ;;
	stop)  stop ;;
	*) echo "usage: $0 {start|wait|stop}" >&2; exit 2 ;;
esac
