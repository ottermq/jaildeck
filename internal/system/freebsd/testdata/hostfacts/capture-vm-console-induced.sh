#!/bin/sh
# Capture a bhyve guest's serial console from the host's end, for the browser
# console (#123, JD-042): what the nmdm device does in its default mode and in
# raw mode, whether an exclusive lock stops a second opener, and what a reader
# sees when the guest reboots or is powered off.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It needs vm-bhyve configured on a
# ZFS datastore, and the path of conprobe, a small Go program that opens the
# device with the same calls Jail Deck would make (open with O_NONBLOCK,
# x/term's MakeRaw, TIOCEXCL), cross-built for FreeBSD, and a ready image at
# <datastore>/.images/debian-13, which it uses and leaves alone. It makes one
# throwaway guest, jd-cap-con, as Jail Deck makes one (JD-038): UEFI, 1 vCPU,
# 512M, a cloud-init seed with no keys, and disk0 a volmode=dev clone of the
# image. Debian's cloud image runs a getty on its serial console, so com1 has a
# login prompt that answers a return. The guest and template are destroyed
# again; nothing is ever typed at the prompt but returns.
#
# Usage: capture-vm-console-induced.sh /path/to/conprobe

PROBE=$1
OUT=/tmp/jd-capture-vm-console
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

[ -x "$PROBE" ] || { echo "capture-vm-console-induced: give the conprobe binary's path." >&2; exit 1; }
if ! $PRIV vm list >/dev/null 2>&1; then
	echo "capture-vm-console-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
ds=$($PRIV vm datastore list | awk '$1 == "default" { print $4 }')
g=jd-cap-con
dev=/dev/nmdm-$g.1B
template="$store/.templates/jd-capture-con.conf"
image="$ds/.images/debian-13@base"
zfs list "$image" >/dev/null 2>&1 || { echo "capture-vm-console-induced: no $image; prepare it first." >&2; exit 1; }
$PRIV vm list | grep -q "^$g " && { echo "capture-vm-console-induced: $g exists; refusing." >&2; exit 1; }
[ -e "$template" ] && { echo "capture-vm-console-induced: $template exists; refusing." >&2; exit 1; }
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
	printf 'nmdm:     %s\n' "$(ls /dev/nmdm-$g.* 2>/dev/null | tr '\n' ' ')"
	printf 'datasets: %s\n' "$([ "$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')" = "$datasets_before" ] && echo as-before || echo CHANGED)"
	printf 'system.conf: %s\n' "$([ "$($PRIV sha256 -q "$store/.config/system.conf")" = "$sys_before" ] && echo byte-identical || echo CHANGED)"
	printf 'rc.conf:  %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
}
trap restore EXIT INT TERM

started() {
	s=$(date +%s)
	while [ ! -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 20 ]; do sleep 1; done
	sleep 4
}

cat > "$OUT/template.conf" <<'EOF'
loader="uefi"
cpu=1
memory=512M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
EOF
$PRIV cp "$OUT/template.conf" "$template"
cap con_create "$PRIV vm create -t jd-capture-con -s 8G -C $g"
cap con_disk "$PRIV zfs destroy $ds/$g/disk0 && $PRIV zfs clone -o volmode=dev -o volsize=8G $image $ds/$g/disk0"
cap con_devices_stopped "ls -l /dev/nmdm-$g.* 2>&1"
cap con_start "$PRIV vm start $g"
started
# Booted far enough for the getty. With no NIC, Debian's boot waits out
# systemd-networkd-wait-online's two minutes before it starts one (measured:
# the serial line went quiet there), so three minutes.
sleep 180
cap con_devices_running "ls -l /dev/nmdm-$g.*"
cap con_info "$PRIV vm info $g"

# Each a fresh open, with a reader draining the whole time as a bridge would,
# and three returns typed: raw from the start; then left in the default mode;
# then raw again, to see whether the guest got over it.
cap con_raw "$PRIV $PROBE $dev rawduplex"
cap con_cooked "$PRIV $PROBE $dev cookedduplex"
sleep 5
cap con_raw_again "$PRIV $PROBE $dev rawduplex"

# Exclusive: a second opener while the probe holds TIOCEXCL. Root ignores the
# flag in FreeBSD's tty layer (priv_check), and Jail Deck and vm console both
# run as root -- measured, not assumed.
$PRIV $PROBE $dev excl >"$OUT/con_excl.stdout" 2>"$OUT/con_excl.stderr" &
probe=$!
sleep 2
cap con_excl_cu "$PRIV timeout 3 cu -l $dev </dev/null"
wait $probe
echo $? >"$OUT/con_excl.exit"
printf '%s\t%s\texit=%s\t%ss\n' con_excl "conprobe $dev excl" "$(cat "$OUT/con_excl.exit")" 10 >>"$OUT/MANIFEST"

# A guest reboot: bhyve exits for a reset and vm-bhyve starts it again. Does a
# descriptor opened before it still work after?
$PRIV $PROBE $dev hold >"$OUT/con_reboot.stdout" 2>"$OUT/con_reboot.stderr" &
probe=$!
sleep 2
cap con_reset "$PRIV bhyvectl --vm=$g --force-reset"
sleep 10
cap con_after_reset "$PRIV vm list | awk 'NR == 1 || \$1 == \"$g\"'; ls -l /dev/nmdm-$g.*"
wait $probe
echo $? >"$OUT/con_reboot.exit"
printf '%s\t%s\texit=%s\t%ss\n' con_reboot "conprobe $dev hold (across a reset)" "$(cat "$OUT/con_reboot.exit")" 25 >>"$OUT/MANIFEST"

# Powered off while a reader is waiting.
$PRIV $PROBE $dev stop >"$OUT/con_stop.stdout" 2>"$OUT/con_stop.stderr" &
probe=$!
sleep 2
cap con_poweroff "$PRIV vm poweroff -f $g"
wait $probe
echo $? >"$OUT/con_stop.exit"
printf '%s\t%s\texit=%s\t%ss\n' con_stop "conprobe $dev stop" "$(cat "$OUT/con_stop.exit")" 0 >>"$OUT/MANIFEST"
s=$(date +%s)
while [ -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
cap con_devices_after "ls -l /dev/nmdm-$g.* 2>&1"
cap con_open_after "$PRIV $PROBE $dev rawduplex"
