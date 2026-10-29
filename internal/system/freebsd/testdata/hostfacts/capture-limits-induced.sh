#!/bin/sh
# Capture changing a running jail's resource limits, for #119.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It starts a throwaway jail,
# jdcap119, rooted at the host's / with no network and no dataset of its own,
# so snapshot and replication jobs have nothing of it to copy. It adds, changes
# and removes rctl rules keyed to that name only, pins and unpins its cpuset,
# and runs a few processes inside to see what a lowered limit does to them. On
# exit, even if interrupted, the jail is removed and its rules cleared; every
# other rule on the host is counted before and after.
#
# Usage: capture-limits-induced.sh
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

J=jdcap119
OUT=/tmp/jd-capture-limits
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

[ "$(sysctl -n kern.racct.enable 2>/dev/null)" = 1 ] || { echo "capture-limits-induced: kern.racct.enable is not 1; refusing." >&2; exit 1; }
jls -j $J >/dev/null 2>&1 && { echo "capture-limits-induced: jail $J exists; refusing." >&2; exit 1; }
[ -z "$($PRIV rctl jail:$J)" ] || { echo "capture-limits-induced: rules are keyed to $J already; refusing." >&2; exit 1; }

others_before=$($PRIV rctl | grep -vc "^jail:$J:")
restore() {
	echo "--- restoring"
	$PRIV jail -r $J >/dev/null 2>&1
	$PRIV rctl -r jail:$J >/dev/null 2>&1
	printf 'jail:        %s\n' "$(jls -j $J name 2>&1)"
	printf 'its rules:   %s\n' "$($PRIV rctl jail:$J | wc -l | tr -d ' ')"
	printf 'other rules: %s before, %s after\n' "$others_before" "$($PRIV rctl | grep -vc "^jail:$J:")"
}
trap restore EXIT INT TERM

rules() { cap "$1" "$PRIV rctl jail:$J"; }
usage() { cap "$1" "$PRIV rctl -u jail:$J | grep -E '^(memoryuse|maxproc|openfiles|pcpu)='"; }
# A process inside that holds about 64M resident: sh keeps the string, and
# the trailing command stops sh from exec'ing sleep and dropping it.
hog() { $PRIV jexec $J /usr/sbin/daemon -f /bin/sh -c 'x=$(head -c 67108864 /dev/zero | tr "\0" a); sleep 600; : "$x"'; }
alive() { cap "$1" "$PRIV pgrep -lj $J | sort -k2"; }

cap lim_start "$PRIV jail -c name=$J path=/ host.hostname=$J ip4=disable ip6=disable persist"
NCPU=$(sysctl -n hw.ncpu)

# 1. Adding a rule for a resource that already has one: replaced, or added?
cap lim_add_1g "$PRIV rctl -a jail:$J:memoryuse:deny=1G"
cap lim_add_2g "$PRIV rctl -a jail:$J:memoryuse:deny=2G"
rules lim_rules_after_2g
cap lim_add_log "$PRIV rctl -a jail:$J:memoryuse:log=3G"
rules lim_rules_other_action

# 2. Removing one resource's rule, leaving the others.
cap lim_add_maxproc "$PRIV rctl -a jail:$J:maxproc:deny=100"
cap lim_remove_deny_only "$PRIV rctl -a jail:$J:memoryuse:deny=1G && $PRIV rctl -a jail:$J:memoryuse:log=3G && $PRIV rctl -r jail:$J:memoryuse:deny"
rules lim_rules_after_remove_deny_only
cap lim_remove_memoryuse "$PRIV rctl -r jail:$J:memoryuse"
rules lim_rules_after_remove_one
cap lim_remove_absent "$PRIV rctl -r jail:$J:openfiles"
cap lim_remove_all "$PRIV rctl -r jail:$J"
rules lim_rules_after_remove_all

# 3. memoryuse lowered below what is in use.
hog; hog
sleep 8
usage lim_usage_hogs
cap lim_mem_below "$PRIV rctl -a jail:$J:memoryuse:deny=32M"
sleep 2
alive lim_mem_below_alive
cap lim_mem_below_new_alloc "$PRIV jexec $J /bin/sh -c 'x=\$(head -c 16777216 /dev/zero | tr \"\\\\0\" a); echo allocated \${#x}'"
cap lim_mem_below_new_proc "$PRIV jexec $J /bin/sh -c '/bin/echo forked inside; /bin/echo and again'"
cap lim_mem_raise "$PRIV rctl -a jail:$J:memoryuse:deny=1G"
cap lim_mem_raised_new_alloc "$PRIV jexec $J /bin/sh -c 'x=\$(head -c 16777216 /dev/zero | tr \"\\\\0\" a); echo allocated \${#x}'"
cap lim_mem_clear "$PRIV rctl -r jail:$J:memoryuse"

# 4. maxproc lowered below how many there are (two hogs, plus their sleeps).
usage lim_usage_procs
cap lim_maxproc_below "$PRIV rctl -a jail:$J:maxproc:deny=1"
alive lim_maxproc_below_alive
cap lim_maxproc_below_new "$PRIV jexec $J /bin/sh -c '/bin/echo forked inside; /bin/echo and again'"
cap lim_maxproc_clear "$PRIV rctl -r jail:$J:maxproc"

# 5. openfiles and pcpu, live.
cap lim_openfiles "$PRIV rctl -a jail:$J:openfiles:deny=1000"
cap lim_pcpu "$PRIV rctl -a jail:$J:pcpu:deny=50"
$PRIV jexec $J /usr/sbin/daemon -f /bin/sh -c 'while :; do :; done'
sleep 20
usage lim_usage_pcpu
cap lim_pcpu_spinner "ps -o pid,%cpu,state,command -J $J | grep 'while :' | grep -v grep"
rules lim_rules_live

# 6. cpuset on a running jail with a busy thread already scheduled.
SPIN=$($PRIV pgrep -j $J -f 'while :' | head -1)
cap lim_cpuset_before "$PRIV cpuset -g -j $J; $PRIV cpuset -g -p $SPIN"
cap lim_cpuset_pin "$PRIV cpuset -l 1 -j $J"
cap lim_cpuset_pinned "$PRIV cpuset -g -j $J; $PRIV cpuset -g -p $SPIN; sleep 2; ps -o pid,cpu,lastcpu -p $SPIN 2>/dev/null || ps -o pid,cpu -p $SPIN"
cap lim_cpuset_all "$PRIV cpuset -l all -j $J"
cap lim_cpuset_range "$PRIV cpuset -l 0-$((NCPU - 1)) -j $J"
cap lim_cpuset_unpinned "$PRIV cpuset -g -j $J; $PRIV cpuset -g -p $SPIN"
cap lim_cpuset_too_many "$PRIV cpuset -l $NCPU -j $J"

# 7. A rule for a name no jail answers to: kept for when one starts?
$PRIV jail -r $J
cap lim_stopped_add "$PRIV rctl -a jail:$J:memoryuse:deny=1G"
rules lim_rules_stopped
cap lim_restart "$PRIV jail -c name=$J path=/ host.hostname=$J ip4=disable ip6=disable persist"
cap lim_restart_poststart_twice "$PRIV rctl -a jail:$J:memoryuse:deny=1G"
rules lim_rules_restarted
