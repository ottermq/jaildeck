#!/bin/sh
# Capture what networks per tenant need, for #118 step 2: a bridge per tenant
# against VLANs on one bridge, a jail and a guest on one tenant's network, PF
# keeping tenants apart, and what a few hundred bridges cost.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST'S NETWORKING, and loads PF. It
# refuses to run where PF is already loaded. It needs vm-bhyve on ZFS, a ready
# image at <datastore>/.images/debian-13 and a jail template snapshot (TEMPLATE,
# dataset@snapshot), which it leaves alone. It makes two throwaway tenants,
# A (10.91.1.0/24) and B (10.91.2.0/24), each a bridge with the host on .1, a
# vm-bhyve manual switch on it, a guest (.10) and a VNET jail (.20); the jail
# roots are clones under <pool>/jdcap-tenant, outside anything zrepl copies.
# Then the same members on one VLAN-filtering bridge, then 256 bridges made and
# destroyed. A watchdog unloads PF after ten minutes whatever happens.
# Everything is put back: forwarding as it was, PF unloaded, guests, jails,
# switches, bridges, epairs, datasets and templates destroyed.
#
# Run it detached (daemon(8)); it outlives a dropped SSH session that way.
# Same three files per command as capture.sh: .stdout, .stderr, .exit. Only the
# throwaway interfaces are recorded, never the host's own uplink.
#
# Usage: TEMPLATE=tank/jails/templates/15.1-RELEASE@base capture-tenant-network-induced.sh

OUT=/tmp/jd-capture-tenant
WORK=/var/tmp/jd-capture-tenant
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

# step is cap for a shell function of this script: run here, not in sh -c,
# with the same three files.
step() {
	name=$1
	shift
	t0=$(date +%s)
	"$@" >"$OUT/$name.stdout" 2>"$OUT/$name.stderr"
	status=$?
	echo "$status" >"$OUT/$name.exit"
	printf '%s\t%s\texit=%s\t%ss\n' "$name" "$*" "$status" "$(( $(date +%s) - t0 ))" >>"$OUT/MANIFEST"
	return $status
}

[ -n "$TEMPLATE" ] || { echo "capture-tenant-network-induced: set TEMPLATE=dataset@snapshot." >&2; exit 1; }
zfs list -H -o name "$TEMPLATE" >/dev/null 2>&1 || { echo "capture-tenant-network-induced: no $TEMPLATE." >&2; exit 1; }
if $PRIV pfctl -s info >/dev/null 2>&1; then
	echo "capture-tenant-network-induced: PF is loaded here; refusing to touch it." >&2
	exit 1
fi
netstat -rn -f inet | grep -q '^10\.91\.' && { echo "capture-tenant-network-induced: 10.91.0.0/16 is routed; refusing." >&2; exit 1; }
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
ds=$($PRIV vm datastore list | awk '$1 == "default" { print $4 }')
pool=${ds%%/*}
image="$ds/.images/debian-13@base"
zfs list "$image" >/dev/null 2>&1 || { echo "capture-tenant-network-induced: no $image." >&2; exit 1; }
roots="$pool/jdcap-tenant"
template="$store/.templates/jd-capture-tenant.conf"
for g in jdt-a1 jdt-b1; do $PRIV vm list | grep -q "^$g " && { echo "refusing: $g exists" >&2; exit 1; }; done
for j in jdt-ja jdt-jb; do jls -j $j >/dev/null 2>&1 && { echo "refusing: $j runs" >&2; exit 1; }; done
zfs list -H -o name "$roots" >/dev/null 2>&1 && { echo "refusing: $roots exists" >&2; exit 1; }

forwarding_before=$(sysctl -n net.inet.ip.forwarding)
rc_before=$($PRIV sha256 -q /etc/rc.conf)
sys_before=$($PRIV sha256 -q "$store/.config/system.conf")
ifaces_before=$(ifconfig -l)

restore() {
	echo "--- restoring"
	for g in jdt-a1 jdt-b1; do
		$PRIV vm poweroff -f $g >/dev/null 2>&1
		s=$(date +%s); while [ -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
		$PRIV vm destroy -f $g >/dev/null 2>&1
	done
	for j in jdt-ja jdt-jb; do $PRIV jail -r $j >/dev/null 2>&1; done
	for sw in jdt-a jdt-b jdt-v; do $PRIV vm switch destroy $sw >/dev/null 2>&1; done
	$PRIV rm -f "$template"
	$PRIV pfctl -d >/dev/null 2>&1
	$PRIV pfctl -F all >/dev/null 2>&1
	$PRIV kldunload pf >/dev/null 2>&1
	$PRIV sysctl net.inet.ip.forwarding=$forwarding_before >/dev/null
	# Whatever this made and is still there: bridges, epairs, vlans.
	for i in $(ifconfig -l); do
		case " $ifaces_before " in *" $i "*) ;; *) $PRIV ifconfig $i destroy 2>/dev/null ;; esac
	done
	$PRIV zfs destroy -r "$roots" >/dev/null 2>&1
	rm -rf "$WORK"
	printf 'guests:      %s\n' "$($PRIV vm list | awk 'NR > 1 { print $1 }' | tr '\n' ' ')"
	printf 'jails:       %s\n' "$(jls -N name | tr '\n' ' ')"
	printf 'interfaces:  %s\n' "$([ "$(ifconfig -l)" = "$ifaces_before" ] && echo as-before || echo "CHANGED: $(ifconfig -l)")"
	printf 'forwarding:  %s (was %s)\n' "$(sysctl -n net.inet.ip.forwarding)" "$forwarding_before"
	printf 'pf:          %s\n' "$($PRIV pfctl -s info >/dev/null 2>&1 && echo LOADED || echo unloaded)"
	printf 'roots:       %s\n' "$(zfs list -H -o name "$roots" 2>/dev/null || echo removed)"
	printf 'system.conf: %s\n' "$([ "$($PRIV sha256 -q "$store/.config/system.conf")" = "$sys_before" ] && echo byte-identical || echo CHANGED)"
	printf 'rc.conf:     %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
}
trap restore EXIT INT TERM HUP

mkdir -p "$WORK"
ssh-keygen -q -t ed25519 -N '' -C jd-capture -f "$WORK/key" || exit 1
SSH="ssh -n -i $WORK/key -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=3"

# Runs a command in a guest, over SSH from the host.
in_guest() { ip=$1; shift; $SSH debian@$ip "$*"; }

# --- the tenants: a bridge each, the host on .1, a manual vm-bhyve switch ----
tenant() {
	t=$1 net=$2
	b=$($PRIV ifconfig bridge create) || exit 1
	$PRIV ifconfig $b name jdt-$t up >/dev/null
	$PRIV ifconfig jdt-$t inet $net.1/24
	$PRIV vm switch create -t manual -b jdt-$t jdt-$t &&
	$PRIV ifconfig jdt-$t | grep -E 'inet |flags'
}
step ten_bridge_a tenant a 10.91.1
step ten_bridge_b tenant b 10.91.2
cap ten_switch_list "$PRIV vm switch list | awk 'NR == 1 || \$1 ~ /^jdt-/'"

# --- a guest on each -------------------------------------------------------
cat > "$WORK/template.conf" <<'EOF'
loader="uefi"
cpu=1
memory=512M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
EOF
$PRIV cp "$WORK/template.conf" "$template"
guest() {
	g=$1 sw=$2 ip=$3 gw=$4
	$PRIV vm create -t jd-capture-tenant -s 5G -C -k "$WORK/key.pub" -n "ip=$ip/24;gateway4=$gw;nameservers=9.9.9.9" $g &&
	$PRIV vm add -d network -s $sw $g &&
	$PRIV zfs destroy $ds/$g/disk0 &&
	$PRIV zfs clone -o volmode=dev -o volsize=5G $image $ds/$g/disk0 &&
	$PRIV vm start $g
}
step ten_guest_a guest jdt-a1 jdt-a 10.91.1.10 10.91.1.1
step ten_guest_b guest jdt-b1 jdt-b 10.91.2.10 10.91.2.1

# --- a VNET jail on each, its epair a member of the tenant's bridge --------
$PRIV zfs create -o mountpoint=none "$roots"
jail_on() {
	j=$1 t=$2 ip=$3 gw=$4
	$PRIV zfs clone -o mountpoint=/$roots/$j "$TEMPLATE" "$roots/$j" &&
	e=$($PRIV ifconfig epair create) && e=${e%a} &&
	$PRIV ifconfig jdt-$t addm ${e}a && $PRIV ifconfig ${e}a up &&
	$PRIV jail -c name=$j path=/$roots/$j host.hostname=$j vnet vnet.interface=${e}b persist allow.raw_sockets &&
	$PRIV jexec $j ifconfig ${e}b inet $ip/24 up &&
	$PRIV jexec $j route -q add default $gw &&
	echo "$j: ${e}a on jdt-$t, ${e}b in the jail as $ip"
}
step ten_jail_a jail_on jdt-ja a 10.91.1.20 10.91.1.1
step ten_jail_b jail_on jdt-jb b 10.91.2.20 10.91.2.1
cap ten_members "$PRIV ifconfig jdt-a | grep member; echo; $PRIV ifconfig jdt-b | grep member"

# Booted far enough for SSH: 16-18 s measured (JD-038), with room.
s=$(date +%s)
until in_guest 10.91.1.10 true 2>/dev/null && in_guest 10.91.2.10 true 2>/dev/null; do
	[ $(( $(date +%s) - s )) -gt 150 ] && break; sleep 2
done
cap ten_reach_guests "echo a1 after $(( $(date +%s) - s ))s; $SSH debian@10.91.1.10 hostname; $SSH debian@10.91.2.10 hostname"

probe_all() {
	p=$1
	# Inside a tenant: jail <-> guest, both ways.
	cap ${p}_ja_to_a1 "$PRIV jexec jdt-ja ping -c 2 -t 2 10.91.1.10 >/dev/null 2>&1; echo \$?"
	cap ${p}_a1_to_ja "$SSH debian@10.91.1.10 'ping -c 2 -W 1 10.91.1.20 >/dev/null 2>&1; echo \$?'"
	cap ${p}_ja_tcp_a1 "$PRIV jexec jdt-ja nc -z -w 2 10.91.1.10 22 >/dev/null 2>&1; echo \$?"
	# Across tenants: guest to guest, jail to jail, guest to the other's jail.
	cap ${p}_a1_to_b1 "$SSH debian@10.91.1.10 'ping -c 2 -W 1 10.91.2.10 >/dev/null 2>&1; echo \$?'"
	cap ${p}_a1_tcp_b1 "$SSH debian@10.91.1.10 'timeout 3 bash -c \"</dev/tcp/10.91.2.10/22\" 2>/dev/null; echo \$?'"
	cap ${p}_ja_to_jb "$PRIV jexec jdt-ja ping -c 2 -t 2 10.91.2.20 >/dev/null 2>&1; echo \$?"
	cap ${p}_a1_to_jb "$SSH debian@10.91.1.10 'ping -c 2 -W 1 10.91.2.20 >/dev/null 2>&1; echo \$?'"
	# The host: its own gateway on the tenant, and the other tenant's.
	cap ${p}_a1_to_gw_a "$SSH debian@10.91.1.10 'ping -c 2 -W 1 10.91.1.1 >/dev/null 2>&1; echo \$?'"
	cap ${p}_a1_to_gw_b "$SSH debian@10.91.1.10 'ping -c 2 -W 1 10.91.2.1 >/dev/null 2>&1; echo \$?'"
	cap ${p}_a1_tcp_host_ssh_b "$SSH debian@10.91.1.10 'timeout 3 bash -c \"</dev/tcp/10.91.2.1/22\" 2>/dev/null; echo \$?'"
	# Out.
	cap ${p}_a1_to_internet "$SSH debian@10.91.1.10 'ping -c 2 -W 2 9.9.9.9 >/dev/null 2>&1; echo \$?'"
	cap ${p}_ja_to_internet "$PRIV jexec jdt-ja ping -c 2 -t 3 9.9.9.9 >/dev/null 2>&1; echo \$?"
}

# --- 1. Bridges per tenant, forwarding on, no PF ---------------------------
cap ten_forwarding_on "$PRIV sysctl net.inet.ip.forwarding=1"
probe_all ten_nopf

# --- 2. PF: tenants apart, each to its own gateway, NAT out ----------------
# The main ruleset only includes the anchors and passes everything without
# keeping state -- keep-state over SSH drops the session that loads it, as
# measured in JD-040. The rules under test are all in jaildeck/tenants.
egress=$(route -n get default 2>/dev/null | awk '/interface:/ { print $2 }')
(
	sleep 600
	$PRIV pfctl -d >/dev/null 2>&1
	$PRIV pfctl -F all >/dev/null 2>&1
) &
watchdog=$!
cat > "$WORK/pf.conf" <<EOF
nat-anchor "jaildeck/*"
anchor "jaildeck/*"
pass all no state
EOF
cat > "$WORK/tenants.conf" <<EOF
nat on $egress inet from { 10.91.1.0/24, 10.91.2.0/24 } to any -> ($egress)
# A tenant reaches its own gateway, and nothing else of the host's or of
# another tenant's.
block in quick on jdt-a inet from 10.91.1.0/24 to { 10.91.2.0/24, self } no state
pass in quick on jdt-a inet from 10.91.1.0/24 to 10.91.1.1 no state
block in quick on jdt-b inet from 10.91.2.0/24 to { 10.91.1.0/24, self } no state
pass in quick on jdt-b inet from 10.91.2.0/24 to 10.91.2.1 no state
EOF
cap ten_pf_load "$PRIV kldload -n pf; $PRIV pfctl -f $WORK/pf.conf && $PRIV pfctl -a jaildeck/tenants -f $WORK/tenants.conf && $PRIV pfctl -e"
cap ten_pf_rules "$PRIV pfctl -a jaildeck/tenants -s rules; $PRIV pfctl -a jaildeck/tenants -s nat"
probe_all ten_pf
# The order of the two quick rules matters: a block of "self" first takes the
# gateway with it. Recorded as written, and once more with the pass first.
cat > "$WORK/tenants-passfirst.conf" <<EOF
nat on $egress inet from { 10.91.1.0/24, 10.91.2.0/24 } to any -> ($egress)
pass in quick on jdt-a inet from 10.91.1.0/24 to 10.91.1.1 no state
block in quick on jdt-a inet from 10.91.1.0/24 to { 10.91.2.0/24, self } no state
pass in quick on jdt-b inet from 10.91.2.0/24 to 10.91.2.1 no state
block in quick on jdt-b inet from 10.91.2.0/24 to { 10.91.1.0/24, self } no state
EOF
cap ten_pf_reload "$PRIV pfctl -a jaildeck/tenants -f $WORK/tenants-passfirst.conf && $PRIV pfctl -a jaildeck/tenants -s rules"
probe_all ten_pfp
cap ten_pf_off "$PRIV pfctl -d; $PRIV pfctl -F all; $PRIV kldunload pf"
kill $watchdog 2>/dev/null

# --- 3. VLANs on one bridge --------------------------------------------------
# The same four members moved onto one VLAN-filtering bridge, tenant A
# untagged in VLAN 101 and B in 102; the host's gateways become vlan(4)
# interfaces on the bridge. Each tenant's bridge loses its members, and its
# address, which moves to the VLAN interface.
tap_of() { $PRIV vm info $1 | awk '/active-device/ { print $2; exit }'; }
epair_of() { $PRIV ifconfig jdt-$1 | awk '/member: epair/ { print $2; exit }'; }
ta=$(tap_of jdt-a1); tb=$(tap_of jdt-b1); ea=$(epair_of a); eb=$(epair_of b)
cap ten_vlan_bridge "b=\$($PRIV ifconfig bridge create) && $PRIV ifconfig \$b name jdt-v vlanfilter up && $PRIV ifconfig jdt-v"
cap ten_vlan_move "
	$PRIV ifconfig jdt-a deletem $ta deletem $ea && $PRIV ifconfig jdt-a inet 10.91.1.1 -alias &&
	$PRIV ifconfig jdt-b deletem $tb deletem $eb && $PRIV ifconfig jdt-b inet 10.91.2.1 -alias &&
	$PRIV ifconfig jdt-v addm $ta ifuntagged $ta 101 addm $ea ifuntagged $ea 101 \
	                     addm $tb ifuntagged $tb 102 addm $eb ifuntagged $eb 102 &&
	$PRIV ifconfig jdt-v"
cap ten_vlan_gateways "
	$PRIV ifconfig vlan101 create vlandev jdt-v vlan 101 inet 10.91.1.1/24 up;
	$PRIV ifconfig vlan102 create vlandev jdt-v vlan 102 inet 10.91.2.1/24 up;
	$PRIV ifconfig vlan101; $PRIV ifconfig vlan102"
probe_all ten_vlan

# A guest reboot: bhyve exits for a reset and vm-bhyve starts it again, with a
# new tap, on its switch's bridge -- jdt-a, not jdt-v, and in no VLAN.
cap ten_vlan_reset "$PRIV bhyvectl --vm=jdt-a1 --force-reset"
s=$(date +%s); until in_guest 10.91.1.10 true 2>/dev/null || [ $(( $(date +%s) - s )) -gt 90 ]; do sleep 2; done
cap ten_vlan_after_reset "echo up after $(( $(date +%s) - s ))s; echo tap now: $(tap_of jdt-a1); $PRIV ifconfig jdt-a | grep member; $PRIV ifconfig jdt-v | grep -A1 member"
cap ten_vlan_after_reset_reach "$SSH debian@10.91.1.10 'ping -c 2 -W 1 10.91.1.20 >/dev/null 2>&1; echo to-ja=\$?; ping -c 2 -W 1 10.91.1.1 >/dev/null 2>&1; echo to-gw=\$?'"

# --- 4. What a few hundred bridges cost -------------------------------------
cap ten_scale_create "t0=\$(date +%s); for i in \$(jot 256); do $PRIV ifconfig bridge create >>$WORK/scale || break; done; echo created \$(wc -l < $WORK/scale) in \$(( \$(date +%s) - t0 ))s; ifconfig -l | wc -w"
cap ten_scale_listing "t0=\$(date +%s); ifconfig >/dev/null; echo ifconfig in \$(( \$(date +%s) - t0 ))s; t0=\$(date +%s); ifconfig -g bridge | wc -l; echo listed in \$(( \$(date +%s) - t0 ))s"
cap ten_scale_destroy "t0=\$(date +%s); for b in \$(cat $WORK/scale); do $PRIV ifconfig \$b destroy; done; echo destroyed in \$(( \$(date +%s) - t0 ))s"
