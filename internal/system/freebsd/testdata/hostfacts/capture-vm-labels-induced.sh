#!/bin/sh
# Capture where a guest's labels can live, for #129: ZFS user properties on
# the guest's dataset, or keys in its vm-bhyve configuration -- what each
# survives, and which tools show it. And vm snapshot, while there is a guest.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It creates one throwaway guest,
# jd-cap-129, that boots no OS (UEFI, an empty sparse disk), labels it both
# ways, starts, stops, renames and snapshots it, then destroys it and its
# template and prints what it restored. Run it on a laboratory box.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-vm-labels
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
	echo "capture-vm-labels-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
if $PRIV vm list | grep -q '^jd-cap-'; then
	echo "capture-vm-labels-induced: jd-cap-* guests already exist; refusing." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
dataset=$($PRIV vm datastore list | awk '$1 == "default" && $2 == "zfs" { print $4 }')
template="$store/.templates/jd-capture.conf"
[ -e "$template" ] && { echo "capture-vm-labels-induced: $template exists; refusing." >&2; exit 1; }
[ -n "$dataset" ] || { echo "capture-vm-labels-induced: the default datastore is not on ZFS; refusing." >&2; exit 1; }
rc_before=$($PRIV sha256 -q /etc/rc.conf)
g=jd-cap-129
r=jd-cap-129r

restore() {
	echo "--- restoring"
	for x in $g $r; do
		$PRIV vm poweroff -f $x >/dev/null 2>&1
		s=$(date +%s)
		while [ -e /dev/vmm/$x ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
		$PRIV vm destroy -f $x >/dev/null 2>&1
	done
	$PRIV rm -f "$template"
	printf 'guests:   %s\n' "$($PRIV vm list | awk 'NR > 1 { print $1 }' | tr '\n' ' ')"
	printf 'datasets: %s\n' "$(zfs list -H -o name -r "$dataset" | grep -c jd-cap)"
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
cap vml_datastore "$PRIV vm datastore list"
cap vml_create "$PRIV vm create -t jd-capture -s 1G $g"
cap vml_layout "zfs list -H -o name,type -r $dataset/$g; ls -A $store/$g"

# A ZFS user property on the guest's dataset.
cap vml_zfs_set "$PRIV zfs set otter:tenant=acme otter:instance=web-01 $dataset/$g"
cap vml_zfs_get "zfs get -H -o name,property,value,source -s local all $dataset/$g"
cap vml_zfs_get_disk "zfs get -H -o name,property,value,source otter:tenant $dataset/$g/disk0"
cap vml_zfs_get_recursive_list "zfs get -H -r -t filesystem -o name,value otter:tenant $dataset"

# A key in the guest's configuration, the way vm-bhyve's own config::set
# writes one.
cap vml_conf_set "$PRIV sysrc -f $store/$g/$g.conf otter_tenant=acme"
cap vml_conf "cat $store/$g/$g.conf"
cap vml_info_stopped "$PRIV vm info $g"
cap vml_list "$PRIV vm list"

# Do both survive a start and a stop, and does an unknown key stop it?
cap vml_start "$PRIV vm start $g </dev/null"
s=$(date +%s); until [ -e /dev/vmm/$g ] || [ $(( $(date +%s) - s )) -gt 30 ]; do sleep 1; done
sleep 2
cap vml_running "$PRIV vm list | awk -v g=$g '\$1 == g'; ls /dev/vmm"
cap vml_info_running "$PRIV vm info $g"
cap vml_zfs_set_running "$PRIV zfs set otter:tier=frontend $dataset/$g"

# vm snapshot of a running guest, then of a stopped one.
cap vml_snapshot_running "$PRIV vm snapshot $g"
cap vml_snapshot_running_forced "$PRIV vm snapshot -f $g"
cap vml_snapshots_after_running "zfs list -H -t snapshot -o name -r $dataset/$g"
cap vml_poweroff "$PRIV vm poweroff -f $g"
s=$(date +%s); while [ -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
cap vml_snapshot_stopped "$PRIV vm snapshot $g@jdcap"
cap vml_snapshots_after_stopped "zfs list -H -t snapshot -o name -r $dataset/$g"
cap vml_after_stop "zfs get -H -o property,value -s local all $dataset/$g; grep otter $store/$g/$g.conf"

# A rename: does each kind of label travel with the guest?
cap vml_rename "$PRIV vm rename $g $r"
cap vml_after_rename "zfs get -H -o name,property,value -s local all $dataset/$r; cat $store/$r/$r.conf; zfs list -H -t snapshot -o name -r $dataset/$r"

# Removing a label each way.
cap vml_zfs_inherit "$PRIV zfs inherit otter:tenant $dataset/$r"
cap vml_conf_remove "$PRIV sysrc -f $store/$r/$r.conf -x otter_tenant"
cap vml_after_remove "zfs get -H -o property,value -s local all $dataset/$r; grep -c otter $store/$r/$r.conf"

# vm destroy takes the dataset, and every label with it.
cap vml_destroy "$PRIV vm destroy -f $r"
cap vml_after_destroy "zfs list -H -o name -r $dataset | grep jd-cap; ls -A $store | grep jd-cap"

printf '\n=== captured to %s ===\n' "$OUT"
