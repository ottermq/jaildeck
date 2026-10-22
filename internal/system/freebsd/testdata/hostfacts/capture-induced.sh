#!/bin/sh
# Capture states a host is not normally in: a bridge, PF loaded and enabled, vmm loaded.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It loads kernel modules, enables PF and
# creates interfaces, then puts everything back and prints what it restored. Run it on a
# laboratory box, never on anything whose state matters. It takes seconds and restores on
# exit even if it is interrupted.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-induced
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

# Everything this script did, undone in reverse. Runs on any exit, including a signal.
restore() {
	echo "--- restoring"
	$PRIV ifconfig bridge1 destroy 2>/dev/null
	$PRIV ifconfig bridge0 destroy 2>/dev/null
	$PRIV pfctl -d 2>/dev/null
	$PRIV kldunload pf 2>/dev/null
	$PRIV kldunload vmm 2>/dev/null
	printf 'interfaces: %s\n' "$(ifconfig -l)"
	printf 'pf:  %s\n' "$(kldstat -m pf 2>&1 | tr -d '\n')"
	printf 'vmm: %s\n' "$(kldstat -m vmm 2>&1 | tr -d '\n')"
}
trap restore EXIT INT TERM

printf -- '--- before\ninterfaces: %s\npf:  %s\nvmm: %s\n' \
	"$(ifconfig -l)" \
	"$(kldstat -m pf 2>&1 | tr -d '\n')" \
	"$(kldstat -m vmm 2>&1 | tr -d '\n')"

# --- PF: loaded-but-disabled, then enabled --------------------------------
# Three states have to be told apart and only the first occurs naturally. No ruleset is
# loaded, so the default ruleset passes everything and this does not cut the connection.
if $PRIV kldload pf 2>/dev/null; then
	cap pf_loaded_kldstat 'kldstat -m pf'
	cap pf_loaded_pfctl_info "$PRIV pfctl -s info"
	cap pf_loaded_service_status "$PRIV service pf status"

	$PRIV pfctl -e 2>/dev/null
	cap pf_enabled_pfctl_info "$PRIV pfctl -s info"
	cap pf_enabled_service_status "$PRIV service pf status"
	cap pf_enabled_pfctl_rules "$PRIV pfctl -s rules"
else
	echo "pf: kldload failed, skipping" >&2
fi

# --- bridges: one, then several, since the check reports a list ------------
if $PRIV ifconfig bridge0 create 2>/dev/null; then
	cap bridge_one_group 'ifconfig -g bridge'
	$PRIV ifconfig bridge1 create 2>/dev/null
	cap bridge_two_group 'ifconfig -g bridge'
	cap bridge_two_list 'ifconfig -l'
	cap bridge_detail 'ifconfig bridge0'
else
	echo "bridge: create failed, skipping" >&2
fi

# --- vmm: the hw.vmm tree only exists once the module is loaded (JD-019) ---
# Expected to fail on a guest without nested virtualisation, which is itself the answer
# the vms check gets on such a host.
if $PRIV kldload vmm 2>/dev/null; then
	cap vmm_loaded_kldstat 'kldstat -m vmm'
	cap vmm_loaded_sysctl 'sysctl hw.vmm'
	cap vmm_loaded_sysctl_maxcpu 'sysctl -n hw.vmm.maxcpu'
	cap vmm_loaded_ls_dev 'ls -la /dev/vmm'
else
	cap vmm_kldload_failed "$PRIV kldload vmm"
fi

# Mask MAC addresses, as capture.sh does: a bridge is given one at creation.
for f in "$OUT"/*.stdout; do
	sed -E 's/([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}/xx:xx:xx:xx:xx:xx/g' "$f" >"$f.masked" &&
		mv "$f.masked" "$f"
done

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
