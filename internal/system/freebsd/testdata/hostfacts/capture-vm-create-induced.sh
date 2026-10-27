#!/bin/sh
# Capture a guest created from an image template the way Jail Deck creates
# one, for VM creation (JD-019, JD-038).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It needs vm-bhyve configured on a
# ZFS datastore and a ready image at <datastore>/.images/debian-13, which it
# uses and leaves alone (capture-vm-images-induced.sh shows how one is made).
# It creates a throwaway switch, jd-capsw, with a host address on it, and one
# guest, jd-cap-made: vm create with a cloud-init seed carrying an SSH key and
# a static address, a NIC added on the switch, and disk0 replaced by a clone of
# the image. It boots it, reaches it over SSH from the host, stops it twice, and
# destroys it, the switch and its template again. Run it on a laboratory box.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit. The
# MANIFEST adds how long each one took.

OUT=/tmp/jd-capture-vm-create
WORK=/var/tmp/jd-capture-vm-create
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
	echo "capture-vm-create-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
ds=$($PRIV vm datastore list | awk '$1 == "default" { print $4 }')
image="$ds/.images/debian-13@base"
zfs list "$image" >/dev/null 2>&1 || { echo "capture-vm-create-induced: no $image; prepare it first." >&2; exit 1; }
g=jd-cap-made
template="$store/.templates/jd-capture.conf"
$PRIV vm list | grep -q '^jd-cap-' && { echo "capture-vm-create-induced: jd-cap-* guests exist; refusing." >&2; exit 1; }
$PRIV vm switch list | grep -q '^jd-capsw ' && { echo "capture-vm-create-induced: switch jd-capsw exists; refusing." >&2; exit 1; }
for p in "$template" "$WORK"; do
	[ -e "$p" ] && { echo "capture-vm-create-induced: $p exists; refusing." >&2; exit 1; }
done
rc_before=$($PRIV sha256 -q /etc/rc.conf)
sys_before=$($PRIV sha256 -q "$store/.config/system.conf")
datasets_before=$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')

restore() {
	echo "--- restoring"
	$PRIV vm poweroff -f $g >/dev/null 2>&1
	s=$(date +%s)
	while [ -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
	$PRIV vm destroy -f $g >/dev/null 2>&1
	$PRIV vm switch destroy jd-capsw >/dev/null 2>&1
	$PRIV rm -f "$template"
	rm -rf "$WORK"
	printf 'guests:   %s\n' "$($PRIV vm list | awk 'NR > 1 { print $1 }' | tr '\n' ' ')"
	printf 'switch:   %s\n' "$($PRIV vm switch list | grep -q '^jd-capsw ' && echo LEFT || echo removed)"
	printf 'bridges:  %s\n' "$(ifconfig -g bridge | tr '\n' ' ')"
	printf 'taps:     %s\n' "$(ifconfig -g tap | tr '\n' ' ')"
	printf 'template: %s\n' "$([ -e "$template" ] && echo LEFT || echo removed)"
	printf 'datasets: %s\n' "$([ "$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')" = "$datasets_before" ] && echo as-before || echo CHANGED)"
	printf 'system.conf: %s\n' "$([ "$($PRIV sha256 -q "$store/.config/system.conf")" = "$sys_before" ] && echo byte-identical || echo CHANGED)"
	printf 'rc.conf:  %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
}
trap restore EXIT INT TERM

mkdir -p "$WORK"
ssh-keygen -q -t ed25519 -N '' -C jd-capture -f "$WORK/key" || exit 1

# The operator's switch, standing in: a bridge with no physical member and an
# address for the host on it, so the guest can be reached and reach nothing
# else.
cap vm_mk_switch_create "$PRIV vm switch create jd-capsw"
cap vm_mk_switch_address "$PRIV vm switch address jd-capsw 10.77.0.1/24"
cap vm_mk_switch_list "$PRIV vm switch list"

# Jail Deck's template: UEFI, one virtio disk, and no NIC -- the NIC is added
# on the switch the request names.
$PRIV sh -c "cat > '$template'" <<'TEMPLATE'
loader="uefi"
cpu=1
memory=512M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE

net="ip=10.77.0.10/24;gateway4=10.77.0.1;nameservers=10.77.0.1;hostname=jd-cap-made"
cap vm_mk_create "$PRIV vm create -t jd-capture -c 2 -m 1G -s 8G -C -k $WORK/key.pub -n '$net' $g" || exit 1
cap vm_mk_conf_created "$PRIV cat $store/$g/$g.conf"
cap vm_mk_seed_files "$PRIV sh -c 'cd $store/$g/.cloud-init && for f in *; do echo \"== \$f\"; cat \"\$f\"; done'"

cap vm_mk_add_network "$PRIV vm add -d network -s jd-capsw $g"
cap vm_mk_conf_networked "$PRIV cat $store/$g/$g.conf"

cap vm_mk_disk_destroy "$PRIV zfs destroy $ds/$g/disk0"
cap vm_mk_disk_clone "$PRIV zfs clone -o volmode=dev -o volsize=8G $image $ds/$g/disk0"
cap vm_mk_datasets "zfs list -H -p -o name,type,volsize,used,origin,volmode -r $ds/$g"
cap vm_mk_list_created "$PRIV vm list"

# What vm-bhyve says to what Jail Deck must refuse before it asks: a name
# taken, a switch that does not exist, a netconfig it cannot read.
cap vm_mk_create_taken "$PRIV vm create -t jd-capture -s 8G $g"
cap vm_mk_add_network_nosuch "$PRIV vm add -d network -s jd-nosuchsw $g"
cap vm_mk_conf_after_nosuch "$PRIV cat $store/$g/$g.conf"
cap vm_mk_create_badnet "$PRIV vm create -t jd-capture -s 8G -C -n 'ip=10.77.0.11/24;mtu=9000' jd-cap-badnet"
cap vm_mk_list_after_refusals "$PRIV vm list"

# Booted: reachable over the switch with the key, at the address in the seed.
$PRIV vm start $g </dev/null >/dev/null 2>&1
t0=$(date +%s)
: >"$OUT/vm_mk_reach.stdout"
until ssh -i "$WORK/key" -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
	-o ConnectTimeout=2 debian@10.77.0.10 true 2>/dev/null; do
	[ $(( $(date +%s) - t0 )) -gt 300 ] && break
	sleep 2
done
reached=$(( $(date +%s) - t0 ))
echo "$reached" >"$OUT/vm_mk_reach.stdout"; echo 0 >"$OUT/vm_mk_reach.exit"; : >"$OUT/vm_mk_reach.stderr"
printf '%s\t%s\texit=0\t%ss\n' vm_mk_reach "(seconds from vm start until ssh answered)" "$reached" >>"$OUT/MANIFEST"
cap vm_mk_inside "ssh -i $WORK/key -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null debian@10.77.0.10 'hostname; ip -br addr; ip route; lsblk -b -o NAME,SIZE,MOUNTPOINT /dev/vda; cat /etc/resolv.conf; sudo cloud-init status'"
cap vm_mk_info_running "$PRIV vm info $g"
cap vm_mk_ping "ping -c 3 -t 5 10.77.0.10"

# watch <capture> <seconds>: one line per second of vm list's row until the
# guest is stopped.
watch() {
	name=$1 n=$2 i=0
	t0=$(date +%s)
	: >"$OUT/$name.stdout"
	while [ $i -lt $n ]; do
		row=$($PRIV vm list | awk -v g=$g '$1 == g { $1 = ""; print }' | sed 's/^ *//')
		printf '%3ds  %s\n' $i "$row" >>"$OUT/$name.stdout"
		case "$row" in *Stopped*) break ;; esac
		i=$((i + 1))
		sleep 1
	done
	echo 0 >"$OUT/$name.exit"; : >"$OUT/$name.stderr"
	printf '%s\t%s\texit=0\t%ss\n' "$name" "(state each second, up to ${n}s)" "$(( $(date +%s) - t0 ))" >>"$OUT/MANIFEST"
}

# A booted guest with its network up answers the power button (JD-019
# measured 4-5 s; JD-037's had no network and was still booting).
cap vm_mk_stop_booted "$PRIV vm stop $g"
watch vm_mk_after_stop_booted 70
$PRIV vm poweroff -f $g >/dev/null 2>&1
while [ -e /dev/vmm/$g ]; do sleep 1; done

# A second boot is not a first: cloud-init has run, so it should be quicker.
$PRIV vm start $g </dev/null >/dev/null 2>&1
t0=$(date +%s)
until ssh -i "$WORK/key" -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
	-o ConnectTimeout=2 debian@10.77.0.10 true 2>/dev/null; do
	[ $(( $(date +%s) - t0 )) -gt 300 ] && break
	sleep 2
done
echo "$(( $(date +%s) - t0 ))" >"$OUT/vm_mk_reach_again.stdout"; echo 0 >"$OUT/vm_mk_reach_again.exit"; : >"$OUT/vm_mk_reach_again.stderr"
printf '%s\t%s\texit=0\t%ss\n' vm_mk_reach_again "(seconds from the second vm start until ssh answered)" "$(cat "$OUT/vm_mk_reach_again.stdout")" >>"$OUT/MANIFEST"

# Destroying a running guest: what vm-bhyve says.
cap vm_mk_destroy_running "$PRIV vm destroy -f $g"
$PRIV vm poweroff -f $g >/dev/null 2>&1
while [ -e /dev/vmm/$g ]; do sleep 1; done
cap vm_mk_destroy "$PRIV vm destroy -f $g"
cap vm_mk_after_destroy "zfs list -H -o name -r $ds; zfs get -H clones $image; ls -A $store"

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
