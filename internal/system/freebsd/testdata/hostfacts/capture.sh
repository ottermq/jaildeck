#!/bin/sh
# Capture the raw output of every command jaildeck's Host Setup readiness report and
# dataset layout need to parse (#1, #21). Read-only: nothing here changes the host.
#
# For each command, three files: .stdout, .stderr, .exit (the exit status).
# Not ".out": the repository's .gitignore excludes *.out as a Go coverage artifact, which
# would silently swallow every capture.
# All three matter -- "pf not loaded" is an empty stdout, a message on stderr and a
# non-zero exit, and a parser that only ever sees stdout cannot tell it from "disabled".
#
# No command here is piped or filtered. system.CommandRunner takes a name and an argument
# array and runs no shell, so the adapter cannot pipe either: a capture that greps is a
# capture of something the adapter can never see. Filter in Go, against the whole output.

OUT=/tmp/jd-capture
rm -rf "$OUT"
mkdir -p "$OUT"
: > "$OUT/MANIFEST"

# privilege helper: some of these are root-only, and jaildeck runs as root
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

# --- provenance -----------------------------------------------------------
cap version_u 'freebsd-version -u'
cap version_k 'freebsd-version -k'
cap uname 'uname -a'

# --- #1: racct ------------------------------------------------------------
cap sysctl_racct 'sysctl -n kern.racct.enable'
cap sysctl_racct_full 'sysctl kern.racct.enable'
cap sysctl_racct_absent 'sysctl -n kern.racct.nosuchknob'
cap rctl_rules "$PRIV rctl"

# --- #1: bridges ----------------------------------------------------------
cap ifconfig_bridge_group 'ifconfig -g bridge'
cap ifconfig_list 'ifconfig -l'

# --- #1: devfs rulesets ---------------------------------------------------
# The kernel is the authority, not the files: a host with no /etc/devfs.rules still has
# the rulesets /etc/defaults/devfs.rules ships, devfsrules_jail=4 among them. The files
# are captured only because they are where the NAMES live; the numbers come from devfs.
cap devfs_rule_showsets "$PRIV devfs rule showsets"
# The same command without privilege. devfs rule is root-only, and its refusal is the one
# "unknown" shape any check here can actually produce -- worth a fixture, not prose.
cap devfs_rule_showsets_unpriv 'devfs rule showsets'
for set in $(cat "$OUT/devfs_rule_showsets.stdout"); do
	cap "devfs_rule_show_$set" "$PRIV devfs rule -s $set show"
done
cap devfs_rules_etc "$PRIV cat /etc/devfs.rules"
cap devfs_rules_defaults "$PRIV cat /etc/defaults/devfs.rules"

# --- #1: cpu virtualization -----------------------------------------------
# The whole buffer. The VT-x line is indented, its feature list varies by CPU, and "UG"
# is a substring of "root HUB", which dmesg prints once per USB controller.
cap dmesg 'dmesg'
cap sysctl_vmm 'sysctl hw.vmm'
cap kldstat 'kldstat'
cap kldstat_vmm 'kldstat -m vmm'

# --- #1: pf ---------------------------------------------------------------
cap pfctl_info "$PRIV pfctl -s info"
cap pfctl_rules "$PRIV pfctl -s rules"
cap kldstat_pf 'kldstat -m pf'
cap service_pf_status "$PRIV service pf status"

# --- #1: exec.timeout -----------------------------------------------------
# /etc/jail.conf is where the handbook puts shared defaults -- and it does not exist on
# every host that runs jails. Capture its absence as carefully as its contents.
cap jail_conf 'cat /etc/jail.conf'
cap jail_conf_d 'ls -la /etc/jail.conf.d'
cap jail_conf_d_cat 'cat /etc/jail.conf.d/*.conf'
cap jls 'jls'
cap service_jail_status "$PRIV service jail status"

# --- #21: pool discovery and the dataset layout ---------------------------
cap zpool_list_h 'zpool list -H -o name'
cap zpool_list 'zpool list'

# Every pool, never a guess. A host can have several, and the order zpool prints them in
# means nothing: on one real host the first is the backup pool, which holds a replica of
# the jails layout with every mountpoint set to none. Picking it would report a host as
# provisioned off datasets mounted nowhere.
for pool in $(cat "$OUT/zpool_list_h.stdout"); do
	cap "zfs_list_r_$pool" "zfs list -H -o name,mountpoint -r $pool"
	cap "zfs_list_r_${pool}_jails" "zfs list -H -o name,mountpoint -r $pool/jails"
done
cap zfs_list_all 'zfs list -H -o name,mountpoint'
cap zfs_list_all_canmount 'zfs list -H -o name,mountpoint,canmount'

# The probes below are about the SHAPE OF FAILURE, not about this host's layout, so any
# existing pool will do and which one it is carries no meaning.
PROBE=$(sed -n 1p "$OUT/zpool_list_h.stdout")
echo "$PROBE" >"$OUT/PROBE_POOL"

# THE IMPORTANT ONE. Naming several datasets where one is missing exits non-zero and
# still prints the others on stdout -- a partial success that a parser treating exit != 0
# as total failure gets wrong. Verified here rather than asserted.
cap zfs_list_mixed "zfs list -H -o name,mountpoint $PROBE $PROBE/definitely-not-here"
cap zfs_list_absent "zfs list -H -o name,mountpoint $PROBE/definitely-not-here"
cap zfs_list_absent_pool 'zfs list -H -o name,mountpoint -r nosuchpool'
cap zfs_get_mountpoint "zfs get -H -o value mountpoint $PROBE"

# --- redaction ------------------------------------------------------------
# dmesg names every NIC's MAC address. Nothing any parser reads is affected, and these
# fixtures live in a public repository, so mask them. This is the only edit the script
# makes to any captured byte, and it is reproducible.
for f in "$OUT"/*.stdout; do
	sed -E 's/([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}/xx:xx:xx:xx:xx:xx/g' "$f" >"$f.masked" &&
		mv "$f.masked" "$f"
done

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
