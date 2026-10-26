#!/bin/sh
# Capture vm-bhyve starting and stopping a guest, for VM lifecycle (JD-019).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It needs vm-bhyve configured and
# answering, creates one throwaway guest, jd-cap-life, that boots no OS, and
# starts, stops and powers it off while watching what vm-bhyve reports --
# then destroys it and its template, and prints what it restored. Run it on
# a laboratory box.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-vm-lifecycle
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
	echo "capture-vm-lifecycle-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
if $PRIV vm list | grep -q '^jd-cap-'; then
	echo "capture-vm-lifecycle-induced: jd-cap-* guests already exist; refusing." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
template="$store/.templates/jd-capture.conf"
[ -e "$template" ] && { echo "capture-vm-lifecycle-induced: $template exists; refusing." >&2; exit 1; }
rc_before=$($PRIV sha256 -q /etc/rc.conf)
g=jd-cap-life

restore() {
	echo "--- restoring"
	$PRIV vm poweroff -f $g >/dev/null 2>&1
	s=$(date +%s)
	while [ -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
	$PRIV vm destroy -f $g >/dev/null 2>&1
	$PRIV rm -f "$template"
	printf 'guests:   %s\n' "$($PRIV vm list | awk 'NR > 1 { print $1 }' | tr '\n' ' ')"
	printf 'vmm:      %s\n' "$(ls /dev/vmm 2>/dev/null | tr '\n' ' ')"
	printf 'vm-run:   %s\n' "$(pgrep -fl "vm _run $g" | grep -v pgrep | wc -l | tr -d ' ')"
	printf 'template: %s\n' "$([ -e "$template" ] && echo LEFT || echo removed)"
	printf 'rc.conf:  %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
}
trap restore EXIT INT TERM

# One guest, no network and no OS: UEFI with an empty disk. It is a running
# bhyve all the same, and one that ignores the ACPI power button, which is
# exactly the guest a stop has to fall back to poweroff for (JD-019).
$PRIV sh -c "cat > '$template'" <<'TEMPLATE'
loader="uefi"
cpu=1
memory=256M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE
$PRIV vm create -t jd-capture -s 1G $g >/dev/null || exit 1

# state <capture>: one line per second for $2 seconds, of vm list's row, the
# instance in /dev/vmm and the vm-run supervisor, so how long a change takes
# to show is in the capture.
watch() {
	name=$1 n=$2 i=0
	: >"$OUT/$name.stdout"
	while [ $i -lt $n ]; do
		printf '%2ds  %s  vmm=%s  vm-run=%s\n' $i \
			"$($PRIV vm list | awk -v g=$g '$1 == g { $1 = ""; print }' | sed 's/^ *//')" \
			"$([ -e /dev/vmm/$g ] && echo yes || echo no)" \
			"$(pgrep -f "vm _run $g" | grep -vx $$ | wc -l | tr -d ' ')" >>"$OUT/$name.stdout"
		i=$((i + 1))
		sleep 1
	done
	echo 0 >"$OUT/$name.exit"; : >"$OUT/$name.stderr"
	printf '%s\t%s\texit=0\n' "$name" "(state each second for ${n}s)" >>"$OUT/MANIFEST"
}

cap vm_life_start_absent "$PRIV vm start jd-cap-nosuch"
cap vm_life_poweroff_stopped "$PRIV vm poweroff -f $g"
cap vm_life_stop_stopped "$PRIV vm stop $g"

# Started with stdout a pipe, timed: whether the supervisor vm start leaves
# behind keeps the caller's output open is whether Jail Deck's runner waits.
t0=$(date +%s)
$PRIV vm start $g 2>&1 </dev/null | cat >"$OUT/vm_life_start_piped.stdout"
echo "$(( $(date +%s) - t0 ))s until the pipe closed" >"$OUT/vm_life_start_piped.stderr"
echo 0 >"$OUT/vm_life_start_piped.exit"
printf '%s\t%s\texit=0\n' vm_life_start_piped "vm start $g | cat, timed" >>"$OUT/MANIFEST"
watch vm_life_after_start 5

# Who the supervisor and bhyve belong to, beside this script's own group and
# session: vm start backgrounds vm-run without job control.
cap vm_life_process_tree "ps -o pid,ppid,pgid,sid,command -p $$; ps -axo pid,ppid,pgid,sid,command | grep -E 'vm _run $g|bhyve: $g' | grep -v grep"

cap vm_life_start_running "$PRIV vm start $g"
cap vm_life_list_running "$PRIV vm list"

cap vm_life_stop "$PRIV vm stop $g"
watch vm_life_after_stop 30

cap vm_life_list_stopped "$PRIV vm list"

# Powered off while running, without a stop first: how long until vm-bhyve
# says Stopped, the instance is gone and vm-run has exited.
$PRIV vm start $g </dev/null >/dev/null 2>&1
s=$(date +%s); until [ -e /dev/vmm/$g ] || [ $(( $(date +%s) - s )) -gt 30 ]; do sleep 1; done
sleep 3
cap vm_life_poweroff "$PRIV vm poweroff -f $g"
watch vm_life_after_poweroff 8

# Stopped in its first second, while the firmware is still starting: JD-019
# measured the power button ignored before a guest's OS is up.
$PRIV vm start $g </dev/null >/dev/null 2>&1
until [ -e /dev/vmm/$g ]; do :; done
cap vm_life_stop_early "$PRIV vm stop $g"
watch vm_life_after_stop_early 15

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
