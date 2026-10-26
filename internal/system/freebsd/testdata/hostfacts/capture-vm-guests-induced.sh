#!/bin/sh
# Capture vm-bhyve listing guests, for the read-only vms domain (JD-019).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It needs vm-bhyve configured and
# answering, creates two throwaway guests named jd-cap-* and a switch,
# jd-capsw, with no physical member, starts one guest on that switch, marks it
# to start at boot, captures how vm-bhyve reports each -- then powers off and
# destroys them, removes its templates, the switch and vm_list, and prints
# what it restored.
# Run it on a laboratory box.
#
# The guests boot no OS: an empty disk and UEFI firmware are a running bhyve
# all the same, which is everything a listing has to show. No image needed.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-vm-guests
rm -rf "$OUT"
mkdir -p "$OUT"
: > "$OUT/MANIFEST"

PRIV=""
if [ "$(id -u)" -ne 0 ]; then
	if command -v doas >/dev/null 2>&1; then PRIV="doas"
	elif command -v sudo >/dev/null 2>&1; then PRIV="sudo -n"; fi
fi

cap() {
	name=$1
	shift
	sh -c "$*" >"$OUT/$name.stdout" 2>"$OUT/$name.stderr"
	status=$?
	echo "$status" >"$OUT/$name.exit"
	printf '%s\t%s\texit=%s\n' "$name" "$*" "$status" >>"$OUT/MANIFEST"
}

if ! $PRIV vm list >/dev/null 2>&1; then
	echo "capture-vm-guests-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
if $PRIV vm list | grep -q '^jd-cap-'; then
	echo "capture-vm-guests-induced: jd-cap-* guests already exist; refusing." >&2
	exit 1
fi
if grep -q '^vm_list=' /etc/rc.conf; then
	# vm_list is the operator's, and this script would have to change it.
	echo "capture-vm-guests-induced: vm_list is set here; refusing." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
template="$store/.templates/jd-capture.conf"
[ -e "$template" ] && { echo "capture-vm-guests-induced: $template exists; refusing." >&2; exit 1; }
if $PRIV vm switch list | grep -q '^jd-capsw '; then
	echo "capture-vm-guests-induced: switch jd-capsw exists; refusing." >&2
	exit 1
fi
rc_before=$($PRIV sha256 -q /etc/rc.conf)

restore() {
	echo "--- restoring"
	$PRIV vm poweroff -f jd-cap-running >/dev/null 2>&1
	s=$(date +%s)
	while $PRIV vm list | grep -q '^jd-cap-running .*Running' && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
	$PRIV vm destroy -f jd-cap-running >/dev/null 2>&1
	$PRIV vm destroy -f jd-cap-stopped >/dev/null 2>&1
	$PRIV rm -f "$template" "$store/.templates/jd-capture-net.conf"
	$PRIV vm switch destroy jd-capsw >/dev/null 2>&1
	$PRIV sysrc -q -x vm_list 2>/dev/null
	printf 'guests:   %s\n' "$($PRIV vm list | awk 'NR > 1 { print $1 }' | tr '\n' ' ')"
	printf 'vmm:      %s\n' "$(ls /dev/vmm 2>/dev/null | tr '\n' ' ')"
	printf 'template: %s\n' "$([ -e "$template" ] && echo LEFT || echo removed)"
	printf 'switch:   %s\n' "$($PRIV vm switch list | grep -q '^jd-capsw ' && echo LEFT || echo removed)"
	printf 'rc.conf:  %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
}
trap restore EXIT INT TERM

# The stopped guest has no network. The running one is on a switch of its
# own with no physical member, so it is on none of the operator's bridges
# and `vm info` still has a network section to show.
$PRIV vm switch create jd-capsw >/dev/null || exit 1
$PRIV sh -c "cat > '$template'" <<'TEMPLATE'
loader="uefi"
cpu=1
memory=256M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE
$PRIV sh -c "cat > '$store/.templates/jd-capture-net.conf'" <<'TEMPLATE'
loader="uefi"
cpu=2
memory=512M
network0_type="virtio-net"
network0_switch="jd-capsw"
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE

cap vm_guests_none_list "$PRIV vm list"
cap vm_guests_none_info "$PRIV vm info"

$PRIV vm create -t jd-capture -s 1G jd-cap-stopped >/dev/null || exit 1
$PRIV vm create -t jd-capture-net -s 1G jd-cap-running >/dev/null || exit 1
$PRIV vm start jd-cap-running </dev/null >/dev/null 2>&1
s=$(date +%s); until [ -e /dev/vmm/jd-cap-running ] || [ $(( $(date +%s) - s )) -gt 30 ]; do sleep 1; done

cap vm_guests_list "$PRIV vm list"
cap vm_guests_list_v "$PRIV vm list -v"
cap vm_guests_info "$PRIV vm info"
cap vm_guests_info_running "$PRIV vm info jd-cap-running"
cap vm_guests_info_stopped "$PRIV vm info jd-cap-stopped"
cap vm_guests_info_absent "$PRIV vm info jd-cap-nosuch"
cap vm_guests_dev_vmm "ls /dev/vmm"
cap vm_guests_conf_running "$PRIV cat $store/jd-cap-running/jd-cap-running.conf"
cap vm_guests_vm_run "ps -axo pid,ppid,command | grep -E 'vm _run jd-cap|bhyve: jd-cap' | grep -v grep"

$PRIV sysrc -q vm_list="jd-cap-running" >/dev/null
cap vm_guests_autostart_list "$PRIV vm list"
cap vm_guests_autostart_info "$PRIV vm info jd-cap-running"

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
