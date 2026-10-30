#!/bin/sh
# Capture what a bridge's ARP table says of its members, for #118 step 3
# (JD-047): whether it can tell Jail Deck which addresses of a network are in
# use, beyond what the jails' and guests' configurations say.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It creates one throwaway bridge,
# jdcap0, with the host's address on it and no physical member, and one
# throwaway VNET jail, jd-cap-arp (path=/, no dataset), on a private port.
# The jail gives itself an address, says nothing, then pings the gateway,
# then gives itself a second address by hand and pings from it. Everything is
# destroyed on exit. Run it on a laboratory box.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-network-addresses
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

J=jd-cap-arp
NET=10.79.0
ifconfig -l | tr ' ' '\n' | grep -qx jdcap0 && { echo "capture-network-addresses-induced: jdcap0 exists; refusing." >&2; exit 1; }
netstat -rn -f inet | grep -q "^$NET" && { echo "capture-network-addresses-induced: $NET.0/24 is routed here; refusing." >&2; exit 1; }
jls -j $J >/dev/null 2>&1 && { echo "capture-network-addresses-induced: $J exists; refusing." >&2; exit 1; }
epairs_before=$(ifconfig -l | tr ' ' '\n' | grep -c '^epair')

restore() {
	echo "--- restoring"
	$PRIV jail -r $J >/dev/null 2>&1
	[ -n "$EA" ] && $PRIV ifconfig "$EA" destroy 2>/dev/null
	$PRIV ifconfig jdcap0 destroy 2>/dev/null
	printf 'bridge:  %s\n' "$(ifconfig -l | tr ' ' '\n' | grep -c '^jdcap0$')"
	printf 'epairs:  %s (were %s)\n' "$(ifconfig -l | tr ' ' '\n' | grep -c '^epair')" "$epairs_before"
	printf 'jail:    %s\n' "$(jls -j $J name 2>/dev/null || echo gone)"
}
trap restore EXIT INT TERM

B=$($PRIV ifconfig bridge create) && $PRIV ifconfig "$B" name jdcap0 up >/dev/null && $PRIV ifconfig jdcap0 inet $NET.1/24
EA=$($PRIV ifconfig epair create)
EB=${EA%a}b
$PRIV ifconfig jdcap0 addm "$EA" private "$EA"
$PRIV ifconfig "$EA" up
$PRIV jail -c name=$J path=/ vnet persist vnet.interface="$EB"
$PRIV jexec $J ifconfig lo0 inet 127.0.0.1/8 up
$PRIV jexec $J ifconfig "$EB" inet $NET.5/24 up

cap nad_max_age "sysctl net.link.ether.inet.max_age"
cap nad_pfil_member "sysctl -n net.link.bridge.pfil_member"
cap nad_arp_quiet "arp -an -i jdcap0"
$PRIV jexec $J ping -c1 -t2 $NET.1 >/dev/null
cap nad_arp_talked "arp -an -i jdcap0"
$PRIV jexec $J ifconfig "$EB" inet $NET.77/24 alias
$PRIV jexec $J ping -c1 -t2 -S $NET.77 $NET.1 >/dev/null
cap nad_arp_selfset "arp -an -i jdcap0"
cap nad_arp_nosuch "arp -an -i jdnope0"
cap nad_bridge "ifconfig -f inet:cidr jdcap0"

echo "captured into $OUT"
