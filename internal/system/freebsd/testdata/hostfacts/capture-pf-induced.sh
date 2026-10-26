#!/bin/sh
# Capture PF with a ruleset actually loaded, for the Firewall Visibility milestone (#16).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It loads pf and pflog, loads a test ruleset,
# enables PF, starts pflogd, then puts every one of those back and prints what it
# restored. Run it on a laboratory box, never on anything whose state matters. It restores
# on exit even if it is interrupted.
#
# capture-induced.sh already covers PF loaded with no rules. What no capture had seen is
# the thing the milestone is for: a real ruleset with counters, anchors, tables with
# addresses in them, and pflog lines.
#
# The ruleset is loaded from a temporary file with `pfctl -f`. /etc/pf.conf is never
# read, written or created (JD-020), and pf_enable/pflog_enable are never touched.
#
# It passes everything, so it cannot cut the connection running it: SSH is passed first
# and quick, and nothing on the address list it blocks is a real host (they are
# documentation ranges, RFC 5737).
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-pf
rm -rf "$OUT"
mkdir -p "$OUT"
: > "$OUT/MANIFEST"

PRIV=""
if [ "$(id -u)" -ne 0 ]; then
	if command -v doas >/dev/null 2>&1; then
		PRIV="doas"
	elif command -v sudo >/dev/null 2>&1; then
		PRIV="sudo -n"
	fi
fi

cap() {
	name=$1
	shift
	sh -c "$*" >"$OUT/$name.stdout" 2>"$OUT/$name.stderr"
	status=$?
	echo "$status" >"$OUT/$name.exit"
	printf '%s\t%s\texit=%s\n' "$name" "$*" "$status" >>"$OUT/MANIFEST"
}

# What was there before, so restore puts back exactly that and not a guess at it.
pf_was_loaded=NO
kldstat -q -m pf && pf_was_loaded=YES
pflog_was_loaded=NO
kldstat -q -m pflog && pflog_was_loaded=YES
pflog_file_existed=NO
[ -e /var/log/pflog ] && pflog_file_existed=YES
if [ "$pf_was_loaded" = YES ] || [ "$pflog_was_loaded" = YES ]; then
	# A host already running PF has a policy of its own, and flushing it to
	# load ours would be exactly the change this project never makes.
	echo "capture-pf-induced: pf or pflog is already loaded here; refusing." >&2
	exit 1
fi

RULES=$(mktemp /tmp/jd-pf-rules.XXXXXX)
ANCHOR_RULES=$(mktemp /tmp/jd-pf-anchor.XXXXXX)

restore() {
	echo "--- restoring"
	$PRIV service pflog onestop >/dev/null 2>&1
	$PRIV pfctl -d >/dev/null 2>&1
	$PRIV pfctl -F all >/dev/null 2>&1
	$PRIV kldunload pflog 2>/dev/null
	$PRIV kldunload pf 2>/dev/null
	[ "$pflog_file_existed" = YES ] || $PRIV rm -f /var/log/pflog
	rm -f "$RULES" "$ANCHOR_RULES"
	printf 'pf:     %s\n' "$(kldstat -m pf 2>&1 | tr -d '\n')"
	printf 'pflog:  %s\n' "$(kldstat -m pflog 2>&1 | tr -d '\n')"
	printf 'pflogd: %s\n' "$(pgrep -x pflogd >/dev/null && echo RUNNING || echo stopped)"
	printf 'file:   %s\n' "$(ls /var/log/pflog 2>&1)"
}
trap restore EXIT INT TERM

ext=$(route -n get default 2>/dev/null | awk '/interface:/ { print $2 }')
[ -n "$ext" ] || ext=$(ifconfig -l | awk '{ print $1 }')
gateway=$(route -n get default 2>/dev/null | awk '/gateway:/ { print $2 }')

# --- not loaded -----------------------------------------------------------
# Every phase-1 command, before the module exists. capture.sh has -s info and
# -s rules in this state; the rest are new.
cap pf_off_anchors "$PRIV pfctl -s Anchors"
cap pf_off_tables "$PRIV pfctl -s Tables"
cap pf_off_table_show "$PRIV pfctl -t jaildeck_test_blocked -T show"
# No pflog file at all: what a host that has never logged anything answers.
if [ "$pflog_file_existed" = NO ]; then
	cap pflog_off_tcpdump "$PRIV tcpdump -n -e -tttt -r /var/log/pflog"
fi

# --- the ruleset ----------------------------------------------------------
# Every shape the milestone has to render: a table with addresses and one without,
# labels, a logged rule, a NAT rule (to see where -s rules puts it), a wildcard anchor
# with rules loaded into it, and an inline anchor.
#
# The logged rule is quick on purpose. PF is last-match-wins: without quick, the later
# `pass out all` also matches the pings and wins, the logged rule never fires, and
# pflog stays empty -- which is exactly what the first run of this script captured.
cat > "$RULES" <<EOF
set skip on lo0
table <jaildeck_test_blocked> persist { 192.0.2.10, 198.51.100.0/24 }
table <jaildeck_test_empty> persist
nat on $ext from 10.99.0.0/24 to any -> ($ext)
pass in quick proto tcp to port 22 label "ssh"
block drop in log quick from <jaildeck_test_blocked> label "blocked-sources"
pass out quick log proto icmp label "icmp-out-logged"
pass out all label "outbound"
pass in all
anchor "jaildeck/*"
anchor "operator" {
	pass in proto icmp label "icmp-inline-anchor"
}
EOF
# The UDP rule is quick and logged so a pflog line comes from inside an anchor: the
# main ruleset's `pass out all` is not quick, so evaluation reaches the anchor.
printf '%s\n' 'pass in proto tcp to port 80 label "web"' \
	'pass out quick log proto udp to port 9 label "udp-anchored-logged"' > "$ANCHOR_RULES"

$PRIV kldload pf || exit 1
cap pf_ruleset_check "$PRIV pfctl -nf $RULES"
$PRIV pfctl -f "$RULES" || exit 1
$PRIV pfctl -a jaildeck/web -f "$ANCHOR_RULES" || exit 1

# Loaded but disabled: a real policy that is doing nothing. The state an operator
# who forgot pf_enable is in, and the one most worth showing plainly.
cap pf_ruleset_disabled_info "$PRIV pfctl -s info"
cap pf_ruleset_disabled_rules "$PRIV pfctl -s rules -v"

$PRIV pfctl -e >/dev/null 2>&1
$PRIV service pflog onestart >/dev/null 2>&1

# Traffic, so counters are not all zero and pflog has lines: the logged ICMP rule
# matches these pings out.
[ -n "$gateway" ] && ping -c 3 -q "$gateway" >/dev/null 2>&1
[ -n "$gateway" ] && echo jaildeck | nc -u -w1 "$gateway" 9 >/dev/null 2>&1
sleep 2

cap pf_ruleset_info "$PRIV pfctl -s info"
cap pf_ruleset_info_v "$PRIV pfctl -v -s info"
cap pf_ruleset_rules "$PRIV pfctl -s rules"
cap pf_ruleset_rules_v "$PRIV pfctl -s rules -v"
cap pf_ruleset_rules_vv "$PRIV pfctl -s rules -vv"
cap pf_ruleset_nat "$PRIV pfctl -s nat -v"
cap pf_ruleset_nat_vv "$PRIV pfctl -s nat -vv"
cap pf_ruleset_anchors "$PRIV pfctl -s Anchors"
cap pf_ruleset_anchors_v "$PRIV pfctl -s Anchors -v"
cap pf_ruleset_anchor_rules "$PRIV pfctl -a jaildeck/web -s rules -v"
cap pf_ruleset_anchor_rules_vv "$PRIV pfctl -a jaildeck/web -s rules -vv"
cap pf_ruleset_anchor_inline "$PRIV pfctl -a operator -s rules -v"
cap pf_ruleset_anchor_parent "$PRIV pfctl -a jaildeck -s rules -v"
cap pf_ruleset_anchor_absent "$PRIV pfctl -a jaildeck/nosuch -s rules -v"

# Every anchor exactly as the view reads it: rules and NAT, -vv, one call each.
# Names carry a slash, which a file name cannot.
for anchor in $($PRIV pfctl -s Anchors 2>/dev/null); do
	file=$(echo "$anchor" | tr / _)
	cap "pf_ruleset_anchor_${file}_rules_vv" "$PRIV pfctl -a $anchor -s rules -vv"
	cap "pf_ruleset_anchor_${file}_nat_vv" "$PRIV pfctl -a $anchor -s nat -vv"
done
cap pf_ruleset_tables "$PRIV pfctl -s Tables"
cap pf_ruleset_tables_v "$PRIV pfctl -s Tables -v"
cap pf_ruleset_tables_vv "$PRIV pfctl -s Tables -vv"
cap pf_ruleset_table_show "$PRIV pfctl -t jaildeck_test_blocked -T show"
cap pf_ruleset_table_show_v "$PRIV pfctl -t jaildeck_test_blocked -T show -v"
cap pf_ruleset_table_empty "$PRIV pfctl -t jaildeck_test_empty -T show"
cap pf_ruleset_table_absent "$PRIV pfctl -t jaildeck_nosuch -T show"
cap pf_ruleset_unpriv 'pfctl -s info'

# pflog is a pcap file written by pflogd, read back with tcpdump: there is no
# pfctl command for it. pflogd buffers, so the file lags the packets; it is read
# once as it stands and once after SIGALRM, which pflogd(8) documents as a flush.
# Where the log is and how often it is flushed are rc.conf's to say. Read, never set.
cap pflog_sysrc 'sysrc -n pflog_logfile pflog_flags'
cap pflog_tcpdump_unflushed "$PRIV tcpdump -n -e -tttt -r /var/log/pflog"
$PRIV pkill -ALRM -x pflogd
sleep 1
cap pflog_tcpdump "$PRIV tcpdump -n -e -tttt -r /var/log/pflog"
cap pflog_tcpdump_unpriv 'tcpdump -n -e -tttt -r /var/log/pflog'

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
