#!/bin/sh
# Capture the host's networks with something on them, for the Networks view (#128).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. On a host with vm-bhyve and
# Jail Deck's default network (the jaildeck switch) it makes, and on exit
# removes, even if interrupted:
#   - a throwaway guest, jd-cap-128, with one NIC on the jaildeck switch,
#     started so that its tap exists (an empty disk: it boots to nothing);
#   - an epair described jail:jd-cap-128, its a side on the operator's switch
#     SWITCH (default vmlan) -- what a VNET jail's start does (JD-030);
#   - a standard switch, jdcap128, whose bridge is then destroyed, as a
#     host has it before vm init has made the bridges at boot.
#
# The capture is `ifconfig -a -f inet:cidr`, which the view reads everything
# from, and `vm switch list`. Interfaces that are neither a bridge nor on one
# are kept but their addresses are replaced with documentation ones
# (RFC 5737), their IPv6 addresses dropped, and VPN tunnels left out: the
# repository is public.
#
# Usage: capture-topology-induced.sh [switch]

SWITCH=${1:-vmlan}
OUT=/tmp/jd-capture-topology
g=jd-cap-128
sw=jdcap128
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

$PRIV vm list | awk 'NR>1{print $1}' | grep -qx $g && { echo "capture-topology-induced: $g exists; refusing." >&2; exit 1; }
$PRIV vm switch list | awk 'NR>1{print $1}' | grep -qx $sw && { echo "capture-topology-induced: $sw exists; refusing." >&2; exit 1; }
bridge=$($PRIV vm switch list | awk -v s=$SWITCH '$1==s{print $3}')
[ -n "$bridge" ] || { echo "capture-topology-induced: no switch $SWITCH." >&2; exit 1; }

epair=""
restore() {
	echo "--- restoring"
	$PRIV vm poweroff -f $g >/dev/null 2>&1; sleep 2
	$PRIV vm destroy -f $g >/dev/null 2>&1
	[ -n "$epair" ] && $PRIV ifconfig ${epair}a destroy 2>/dev/null
	# vm switch destroy fails on a switch whose bridge is gone ("failed to
	# remove virtual switch", measured); vm init makes it again, as at boot,
	# tagged the way vm-bhyve finds it.
	$PRIV vm init >/dev/null 2>&1
	$PRIV vm switch destroy $sw >/dev/null 2>&1
	printf 'guests:   %s\n' "$($PRIV vm list | awk 'NR>1{print $1}' | tr '\n' ' ')"
	printf 'switches: %s\n' "$($PRIV vm switch list | awk 'NR>1{print $1}' | tr '\n' ' ')"
	printf 'epairs:   %s\n' "$(ifconfig -l | tr ' ' '\n' | grep -c '^epair')"
}
trap restore EXIT INT TERM

cap top_guest "$PRIV vm create -t jaildeck -s 64M $g && $PRIV vm add -d network -s jaildeck $g && $PRIV vm start $g"
sleep 3
epair=$($PRIV ifconfig epair create | sed 's/a$//')
cap top_epair "$PRIV ifconfig ${epair}a description jail:jd-cap-128 up && $PRIV ifconfig $bridge addm ${epair}a"
cap top_switch "$PRIV vm switch create $sw && $PRIV ifconfig vm-$sw destroy"

cap top_switch_list "$PRIV vm switch list"
# Every interface, with addresses outside the VM networks replaced and
# tunnels left out.
cap top_ifconfig "ifconfig -a -f inet:cidr | awk '
	/^[^ \t]/ { keep = (\$1 !~ /^(tailscale|tun|wg|ipsec)/); phys = (\$1 ~ /^(em|igb|ix|re|vtnet|bge)[0-9]/) }
	keep && phys && /^\tinet / { sub(/inet [0-9.\\/]+/, \"inet 192.0.2.\" (++n) \"/24\"); sub(/broadcast [0-9.]+/, \"broadcast 192.0.2.255\") }
	keep && phys && /^\tether / { sub(/ether [0-9a-f:]+/, \"ether 00:00:5e:00:53:0\" (++m)) }
	keep && phys && /^\tinet6 / { next }
	keep { print }'"
