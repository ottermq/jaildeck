#!/bin/sh
# Capture pinning a guest's virtual CPUs to host CPUs, for the VM cpuset
# (JD-019, JD-041): vm-bhyve has no setting of its own for it, and passes
# bhyve_options through to bhyve, whose -p vcpu:hostcpu pins one vCPU.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It needs vm-bhyve configured on a
# ZFS datastore and at least four host CPUs. It makes one throwaway guest,
# jd-cap-pin, from its own template (UEFI, 2 vCPUs, 256M, an empty 1G disk:
# the firmware runs and finds nothing to boot, which is enough to have vCPU
# threads), writes bhyve_options into its configuration the way vm-bhyve's own
# config::set does (sysrc -f), starts it, and reads each vCPU thread's cpuset.
# Then a host CPU that does not exist, and a pin of one vCPU out of two.
# Everything is destroyed again. Run it on a laboratory box.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-vm-pin
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
	t0=$(date +%s)
	sh -c "$*" >"$OUT/$name.stdout" 2>"$OUT/$name.stderr"
	status=$?
	echo "$status" >"$OUT/$name.exit"
	printf '%s\t%s\texit=%s\t%ss\n' "$name" "$*" "$status" "$(( $(date +%s) - t0 ))" >>"$OUT/MANIFEST"
	return $status
}

if ! $PRIV vm list >/dev/null 2>&1; then
	echo "capture-vm-pin-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
[ "$(sysctl -n hw.ncpu)" -ge 4 ] || { echo "capture-vm-pin-induced: needs four host CPUs." >&2; exit 1; }
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
ds=$($PRIV vm datastore list | awk '$1 == "default" { print $4 }')
g=jd-cap-pin
conf="$store/$g/$g.conf"
template="$store/.templates/jd-capture-pin.conf"
$PRIV vm list | grep -q "^$g " && { echo "capture-vm-pin-induced: $g exists; refusing." >&2; exit 1; }
[ -e "$template" ] && { echo "capture-vm-pin-induced: $template exists; refusing." >&2; exit 1; }
rc_before=$($PRIV sha256 -q /etc/rc.conf)
sys_before=$($PRIV sha256 -q "$store/.config/system.conf")
datasets_before=$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')

poweroff() {
	$PRIV vm poweroff -f $g >/dev/null 2>&1
	s=$(date +%s)
	while [ -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
}

restore() {
	echo "--- restoring"
	poweroff
	$PRIV vm destroy -f $g >/dev/null 2>&1
	$PRIV rm -f "$template"
	printf 'guests:   %s\n' "$($PRIV vm list | awk 'NR > 1 { print $1 }' | tr '\n' ' ')"
	printf 'template: %s\n' "$([ -e "$template" ] && echo LEFT || echo removed)"
	printf 'datasets: %s\n' "$([ "$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')" = "$datasets_before" ] && echo as-before || echo CHANGED)"
	printf 'system.conf: %s\n' "$([ "$($PRIV sha256 -q "$store/.config/system.conf")" = "$sys_before" ] && echo byte-identical || echo CHANGED)"
	printf 'rc.conf:  %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
}
trap restore EXIT INT TERM

# Starts the guest and waits for its vCPU threads; then records the bhyve
# command line, the threads, and each vCPU thread's cpuset.
run() {
	label=$1
	cap ${label}_start "$PRIV vm start $g"
	s=$(date +%s)
	pid=""
	while [ -z "$pid" ] && [ $(( $(date +%s) - s )) -lt 20 ]; do
		sleep 1
		pid=$(pgrep -f "^bhyve: $g\$")
	done
	sleep 2
	cap ${label}_running "[ -e /dev/vmm/$g ] && echo running || echo 'not running'"
	cap ${label}_log "$PRIV tail -n 8 $store/$g/vm-bhyve.log"
	[ -n "$pid" ] || return 0
	cap ${label}_args "ps -o args= -p $pid"
	cap ${label}_threads "procstat -t $pid"
	# Thread names hold a space ("vcpu 0"), so procstat's columns 4 and 5.
	procstat -t $pid | awk '$4 == "vcpu" { print $2, $5 }' | while read -r tid vcpu; do
		printf 'vcpu %s: %s\n' "$vcpu" "$($PRIV cpuset -g -t "$tid")"
	done >"$OUT/${label}_cpusets.stdout" 2>"$OUT/${label}_cpusets.stderr"
	echo 0 >"$OUT/${label}_cpusets.exit"
	printf '%s\t%s\texit=0\t0s\n' "${label}_cpusets" "cpuset -g -t <each vcpu thread>" >>"$OUT/MANIFEST"
	poweroff
}

cap pin_ncpu "sysctl -n hw.ncpu"
cap pin_rootset "cpuset -g -s 1"
cat > "$OUT/template.conf" <<'EOF'
loader="uefi"
cpu=2
memory=256M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
EOF
$PRIV cp "$OUT/template.conf" "$template"
cap pin_create "$PRIV vm create -t jd-capture-pin -s 1G $g"

# Unpinned first: what vm-bhyve gives bhyve on its own.
run unpinned

# vCPU 0 on host CPU 2, vCPU 1 on host CPU 3.
cap pin_set "$PRIV sysrc -f $conf 'bhyve_options=-p 0:2 -p 1:3'"
cap pin_conf "$PRIV cat $conf"
run pinned

# A host CPU that does not exist.
cap pin_set_bad "$PRIV sysrc -f $conf 'bhyve_options=-p 0:2 -p 1:999'"
run badcpu

# One vCPU pinned, the other left alone.
cap pin_set_one "$PRIV sysrc -f $conf 'bhyve_options=-p 0:2'"
run onepin

# Taking the setting away again.
cap pin_unset "$PRIV sysrc -x -f $conf bhyve_options"
cap pin_conf_unset "$PRIV cat $conf"
