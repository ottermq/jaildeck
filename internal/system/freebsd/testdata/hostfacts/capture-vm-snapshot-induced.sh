#!/bin/sh
# Capture what a guest snapshot through ZFS does, for #174: zfs snapshot -r on
# a running guest's dataset, which is what vm snapshot -f runs -- what it
# takes, whether the guest and vm-bhyve notice, and what a destroy does with
# the snapshots afterwards.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It creates one throwaway guest,
# jd-cap-174, that boots no OS (UEFI, an empty sparse disk), starts and
# snapshots it, then destroys it and its template and prints what it
# restored. Run it on a laboratory box.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-vm-snapshot
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
	echo "capture-vm-snapshot-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
if $PRIV vm list | grep -q '^jd-cap-'; then
	echo "capture-vm-snapshot-induced: jd-cap-* guests already exist; refusing." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
dataset=$($PRIV vm datastore list | awk '$1 == "default" && $2 == "zfs" { print $4 }')
template="$store/.templates/jd-capture.conf"
[ -e "$template" ] && { echo "capture-vm-snapshot-induced: $template exists; refusing." >&2; exit 1; }
[ -n "$dataset" ] || { echo "capture-vm-snapshot-induced: the default datastore is not on ZFS; refusing." >&2; exit 1; }
rc_before=$($PRIV sha256 -q /etc/rc.conf)
g=jd-cap-174

restore() {
	echo "--- restoring"
	$PRIV vm poweroff -f $g >/dev/null 2>&1
	s=$(date +%s)
	while [ -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
	$PRIV vm destroy -f $g >/dev/null 2>&1
	$PRIV rm -f "$template"
	printf 'guests:   %s\n' "$($PRIV vm list | awk 'NR > 1 { print $1 }' | tr '\n' ' ')"
	printf 'datasets: %s\n' "$(zfs list -H -t all -o name -r "$dataset" | grep -c jd-cap)"
	printf 'vmm:      %s\n' "$(ls /dev/vmm 2>/dev/null | tr '\n' ' ')"
	printf 'template: %s\n' "$([ -e "$template" ] && echo LEFT || echo removed)"
	printf 'rc.conf:  %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
}
trap restore EXIT INT TERM

$PRIV sh -c "cat > '$template'" <<'TEMPLATE'
loader="uefi"
cpu=1
memory=256M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE
cap vsn_create "$PRIV vm create -t jd-capture -s 1G $g"
cap vsn_start "$PRIV vm start $g; sleep 3; ls /dev/vmm"

# What Jail Deck will run on a running guest: the guest's dataset, recursively.
cap vsn_zfs_snapshot_running "$PRIV zfs snapshot -r $dataset/$g@jd-a"
cap vsn_list_after "zfs list -H -t snapshot -o name,used,refer -r $dataset/$g"
cap vsn_still_running "$PRIV vm list | awk '\$1 == \"$g\"'; ls /dev/vmm"
cap vsn_info_after "$PRIV vm info $g"
# The same name twice: zfs refuses, which is why the service checks first.
cap vsn_zfs_snapshot_again "$PRIV zfs snapshot -r $dataset/$g@jd-a"
# vm-bhyve's own, for comparison: the same two snapshots under its name.
cap vsn_vm_snapshot_forced "$PRIV vm snapshot -f $g@jd-b"
cap vsn_list_both "zfs list -H -t snapshot -o name -r $dataset/$g"

# A destroy takes them with the guest.
cap vsn_poweroff "$PRIV vm poweroff -f $g; sleep 3; ls /dev/vmm"
cap vsn_destroy "$PRIV vm destroy -f $g"
cap vsn_after_destroy "zfs list -H -t all -o name -r $dataset | grep $g"

echo "captured into $OUT"
