#!/bin/sh
# Capture the raw output of every command jaildeck's Host Setup readiness report and
# dataset layout need to parse. Read-only: nothing here changes the host.
#
# For each command, three files: .stdout, .stderr, .exit (the exit status).
# Not ".out": the repository's .gitignore excludes *.out as a Go coverage artifact, which
# would silently swallow every capture.
# All three matter -- "pf not loaded" is an empty stdout, a message on stderr and a
# non-zero exit, and a parser that only ever sees stdout cannot tell it from "disabled".

OUT=/tmp/jd-capture
rm -rf "$OUT"
mkdir -p "$OUT"
: > "$OUT/MANIFEST"

# privilege helper: some of these need root
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

# --- #1: readiness checks -------------------------------------------------
cap sysctl_racct 'sysctl -n kern.racct.enable'
cap sysctl_racct_full 'sysctl kern.racct.enable'
cap sysctl_racct_absent 'sysctl -n kern.racct.nosuchknob'

cap ifconfig_bridge_group 'ifconfig -g bridge'
cap ifconfig_list 'ifconfig -l'

cap devfs_rules_etc 'cat /etc/devfs.rules'
cap devfs_rules_defaults 'cat /etc/defaults/devfs.rules'
cap devfs_rules_headers 'grep -h "^\[" /etc/devfs.rules /etc/defaults/devfs.rules'

# dmesg's VT-x line. Both the grep and the raw neighbourhood, because the grep tells us
# the shape and the neighbourhood tells us whether anything nearby could be mistaken for it.
cap dmesg_vtx 'dmesg | grep -iE "VT-x|SVM|EPT|UG"'
cap dmesg_cpu_head 'dmesg | grep -iE "^CPU|Features|VT-x|AMD" | head -20'
cap sysctl_vmm 'sysctl hw.vmm'

cap pfctl_info "$PRIV pfctl -s info"
cap pfctl_rules "$PRIV pfctl -s rules"
cap kldstat_pf 'kldstat -m pf'
cap service_pf_status "$PRIV service pf status"

cap jail_conf 'cat /etc/jail.conf'
cap jail_conf_d 'ls -la /etc/jail.conf.d'

# --- #21: pool discovery and the dataset layout ---------------------------
cap zpool_list_h 'zpool list -H -o name'
cap zpool_list 'zpool list'

POOL=$(zpool list -H -o name 2>/dev/null | head -1)
echo "$POOL" >"$OUT/POOL"

cap zfs_list_recursive "zfs list -H -o name,mountpoint -r $POOL"
cap zfs_list_jails_r "zfs list -H -o name,mountpoint -r $POOL/jails"

# THE IMPORTANT ONE. The prompt claims that naming several datasets where one is missing
# exits non-zero while still printing the others on stdout -- a partial success that a
# parser treating exit!=0 as total failure would get wrong. Verify it rather than assert it.
cap zfs_list_mixed "zfs list -H -o name,mountpoint $POOL $POOL/definitely-not-here"
cap zfs_list_absent "zfs list -H -o name,mountpoint $POOL/definitely-not-here"
cap zfs_list_absent_pool 'zfs list -H -o name,mountpoint -r nosuchpool'
cap zfs_get_mountpoint "zfs get -H -o value mountpoint $POOL"

# What a real jails layout looks like, if this host has one.
cap zfs_list_all 'zfs list -H -o name,mountpoint'

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
