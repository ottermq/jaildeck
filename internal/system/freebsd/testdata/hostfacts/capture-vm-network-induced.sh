#!/bin/sh
# Capture what a default VM network needs, for #118 step 1: whether a private
# vm-bhyve switch keeps its guests apart, and outbound NAT through a PF anchor
# Jail Deck would own.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST'S NETWORKING, and loads PF. It
# refuses to run where PF is already loaded, so it never touches an operator's
# ruleset. It needs vm-bhyve on ZFS and a ready image at
# <datastore>/.images/debian-13, which it leaves alone. It makes a throwaway
# switch, jd-netsw (10.81.0.0/24, the host on .1), and two guests on it,
# jd-net-a (.10) and jd-net-b (.11), and has them ping each other, the host
# and the internet three ways: switch shared, switch private, and private
# with NAT. PF is loaded with a ruleset that passes everything and only
# includes the jaildeck/* anchors, and a watchdog turns it off after six
# minutes whatever happens. Everything is put back: PF unloaded, forwarding
# as it was, guests, switch and template destroyed.
#
# Run it detached (daemon(8)): it outlives a dropped SSH session that way.
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-vm-network
WORK=/var/tmp/jd-capture-vm-network
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
	return $status
}

say() { echo "$*" >> "$OUT/PROGRESS"; }

kldstat -q -m pf && { echo "capture-vm-network-induced: PF is loaded here; refusing to touch its ruleset." >&2; exit 1; }
$PRIV vm list >/dev/null 2>&1 || { echo "capture-vm-network-induced: vm-bhyve is not answering." >&2; exit 1; }
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
ds=$($PRIV vm datastore list | awk '$1 == "default" { print $4 }')
image="$ds/.images/debian-13@base"
zfs list "$image" >/dev/null 2>&1 || { echo "capture-vm-network-induced: no $image." >&2; exit 1; }
$PRIV vm switch list | grep -q '^jd-netsw ' && { echo "capture-vm-network-induced: jd-netsw exists; refusing." >&2; exit 1; }
$PRIV vm list | grep -q '^jd-net-' && { echo "capture-vm-network-induced: jd-net-* guests exist; refusing." >&2; exit 1; }
template="$store/.templates/jd-capture.conf"
[ -e "$template" ] && { echo "capture-vm-network-induced: $template exists; refusing." >&2; exit 1; }
egress=$(route -n get default | awk '/interface:/ { print $2 }')
[ -n "$egress" ] || { echo "capture-vm-network-induced: no default route." >&2; exit 1; }

rc_before=$($PRIV sha256 -q /etc/rc.conf)
sys_before=$($PRIV sha256 -q "$store/.config/system.conf")
datasets_before=$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')
forwarding_before=$(sysctl -n net.inet.ip.forwarding)

restore() {
	say "restoring"
	$PRIV pfctl -a jaildeck/vmnet -F all >/dev/null 2>&1
	$PRIV pfctl -F all >/dev/null 2>&1
	$PRIV pfctl -d >/dev/null 2>&1
	$PRIV pkill -f "jd-pf-watchdog" >/dev/null 2>&1
	$PRIV kldunload pf >/dev/null 2>&1
	$PRIV sysctl net.inet.ip.forwarding="$forwarding_before" >/dev/null
	for g in jd-net-a jd-net-b; do $PRIV vm poweroff -f $g >/dev/null 2>&1; done
	s=$(date +%s)
	while { [ -e /dev/vmm/jd-net-a ] || [ -e /dev/vmm/jd-net-b ]; } && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
	for g in jd-net-a jd-net-b; do $PRIV vm destroy -f $g >/dev/null 2>&1; done
	$PRIV vm switch destroy jd-netsw >/dev/null 2>&1
	$PRIV rm -f "$template"
	rm -rf "$WORK"
	{
		printf 'pf:          %s\n' "$(kldstat -q -m pf && echo LOADED || echo unloaded)"
		printf 'forwarding:  %s (was %s)\n' "$(sysctl -n net.inet.ip.forwarding)" "$forwarding_before"
		printf 'guests:      %s\n' "$($PRIV vm list | awk 'NR > 1 && /^jd-net-/ { print $1 }' | tr '\n' ' ')"
		printf 'switch:      %s\n' "$($PRIV vm switch list | grep -q '^jd-netsw ' && echo LEFT || echo removed)"
		printf 'datasets:    %s\n' "$([ "$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')" = "$datasets_before" ] && echo as-before || echo CHANGED)"
		printf 'system.conf: %s\n' "$([ "$($PRIV sha256 -q "$store/.config/system.conf")" = "$sys_before" ] && echo byte-identical || echo CHANGED)"
		printf 'rc.conf:     %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
	} > "$OUT/RESTORED"
	say "done"
}
trap restore EXIT INT TERM

mkdir -p "$WORK"
ssh-keygen -q -t ed25519 -N '' -C jd-net -f "$WORK/key" || exit 1
SSH="ssh -n -i $WORK/key -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=3"

say "switch"
cap vm_net_switch_create "$PRIV vm switch create jd-netsw"
cap vm_net_switch_address "$PRIV vm switch address jd-netsw 10.81.0.1/24"

$PRIV sh -c "cat > '$template'" <<'TEMPLATE'
loader="uefi"
cpu=1
memory=512M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE

# Made the way Jail Deck makes a guest (JD-038).
make_guest() {
	g=$1 addr=$2
	$PRIV vm create -t jd-capture -c 1 -m 1G -s 4G -C -k "$WORK/key.pub" \
		-n "ip=$addr/24;gateway4=10.81.0.1;nameservers=9.9.9.9" $g >/dev/null &&
	$PRIV vm add -d network -s jd-netsw $g >/dev/null &&
	$PRIV zfs destroy "$ds/$g/disk0" &&
	$PRIV zfs clone -o volmode=dev -o volsize=4G "$image" "$ds/$g/disk0"
}
say "guests"
make_guest jd-net-a 10.81.0.10 || exit 1
make_guest jd-net-b 10.81.0.11 || exit 1

# boot <guest...>: start each and wait until SSH answers.
boot() {
	for g in "$@"; do $PRIV vm start $g </dev/null >/dev/null 2>&1; done
	for addr in 10.81.0.10 10.81.0.11; do
		s=$(date +%s)
		until $SSH debian@$addr true 2>/dev/null; do
			[ $(( $(date +%s) - s )) -gt 240 ] && { say "no ssh to $addr"; break; }
			sleep 2
		done
	done
}
halt() {
	for g in jd-net-a jd-net-b; do $PRIV vm stop $g >/dev/null 2>&1; done
	s=$(date +%s)
	while { [ -e /dev/vmm/jd-net-a ] || [ -e /dev/vmm/jd-net-b ]; } && [ $(( $(date +%s) - s )) -lt 60 ]; do sleep 1; done
}

# probe <prefix>: what a sees, from inside a.
probe() {
	p=$1
	cap ${p}_a_to_b "$SSH debian@10.81.0.10 'ping -c 3 -W 2 10.81.0.11'"
	cap ${p}_a_to_host "$SSH debian@10.81.0.10 'ping -c 3 -W 2 10.81.0.1'"
	cap ${p}_a_to_internet "$SSH debian@10.81.0.10 'ping -c 3 -W 2 9.9.9.9'"
	cap ${p}_a_dns "$SSH debian@10.81.0.10 'getent hosts deb.debian.org'"
	cap ${p}_a_tcp_b "$SSH debian@10.81.0.10 'timeout 5 bash -c \"</dev/tcp/10.81.0.11/22\" && echo open'"
	cap ${p}_a_neigh "$SSH debian@10.81.0.10 'ip neigh'"
	cap ${p}_bridge "ifconfig vm-jd-netsw"
}

say "shared"
boot jd-net-a jd-net-b
probe vm_net_shared
halt

say "private"
cap vm_net_private_on "$PRIV vm switch private jd-netsw on"
cap vm_net_switch_list_private "$PRIV vm switch list"
boot jd-net-a jd-net-b
probe vm_net_private

say "nat"
# The safety net: whatever happens next, PF is off again in six minutes.
$PRIV daemon -f -t jd-pf-watchdog sh -c 'sleep 360; pfctl -d'
cat > "$WORK/pf.conf" <<PF
# The operator's ruleset, standing in: everything passes, including the
# packets of connections already open, and Jail Deck's anchors are included.
nat-anchor "jaildeck/*"
anchor "jaildeck/*"
pass all flags any
PF
cap vm_net_pf_load "$PRIV kldload pf && $PRIV pfctl -q -f $WORK/pf.conf && $PRIV pfctl -q -e; $PRIV pfctl -s info | head -1"
cap vm_net_pf_anchor "echo 'nat on $egress inet from 10.81.0.0/24 to any -> ($egress)' | $PRIV pfctl -a jaildeck/vmnet -f - && $PRIV pfctl -a jaildeck/vmnet -s nat"
cap vm_net_pf_anchors "$PRIV pfctl -s Anchors -v; $PRIV pfctl -s nat"
cap vm_net_forwarding "$PRIV sysctl net.inet.ip.forwarding=1"
probe vm_net_nat
cap vm_net_pf_states "$PRIV pfctl -s state | grep -c 10.81.0.10"

say "captured"
