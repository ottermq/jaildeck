#!/bin/sh
# Capture vm-bhyve in every state an operator passes through on the way to
# running a VM (JD-019), for the Host Setup checks that report it.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It installs vm-bhyve and
# edk2-bhyve, sets vm_enable and vm_dir in rc.conf, creates a dataset and runs
# `vm init`, capturing what vm-bhyve says at each step -- then removes all of
# it and prints what it restored. Run it on a laboratory box. It refuses where
# vm-bhyve is already installed or configured, because that is an operator's.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

POOL=${1:-tank}
DS="$POOL/jd-vmcap"
MNT=/jd-vmcap
OUT=/tmp/jd-capture-vm
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

if pkg info -e vm-bhyve || pkg info -e edk2-bhyve || grep -qE '^vm_(enable|dir)=' /etc/rc.conf; then
	echo "capture-vm-induced: vm-bhyve is already installed or configured here; refusing." >&2
	exit 1
fi
if zfs list "$DS" >/dev/null 2>&1 || [ -e "$MNT" ]; then
	echo "capture-vm-induced: $DS or $MNT already exists; refusing." >&2
	exit 1
fi
vmm_was_loaded=NO; kldstat -q -m vmm && vmm_was_loaded=YES
nmdm_was_loaded=NO; kldstat -q -m nmdm && nmdm_was_loaded=YES
rc_before=$($PRIV sha256 -q /etc/rc.conf)

restore() {
	echo "--- restoring"
	$PRIV sysrc -q -x vm_enable vm_dir 2>/dev/null
	$PRIV zfs destroy -r "$DS" 2>/dev/null
	$PRIV rmdir "$MNT" 2>/dev/null
	$PRIV pkg delete -y vm-bhyve edk2-bhyve >/dev/null 2>&1
	[ "$vmm_was_loaded" = YES ] || $PRIV kldunload vmm 2>/dev/null
	[ "$nmdm_was_loaded" = YES ] || $PRIV kldunload nmdm 2>/dev/null
	printf 'packages: %s\n' "$(pkg info -e vm-bhyve && echo vm-bhyve; pkg info -e edk2-bhyve && echo edk2-bhyve)"
	printf 'rc.conf:  %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
	printf 'dataset:  %s\n' "$(zfs list -H -o name "$DS" 2>&1)"
	printf 'vmm:      %s\n' "$(kldstat -q -m vmm && echo loaded || echo not loaded)"
}
trap restore EXIT INT TERM

# --- 0: nothing installed --------------------------------------------------
# What every host but one looks like today.
cap vm_absent_version "$PRIV vm version"
cap vm_absent_sysrc "sysrc -n vm_enable vm_dir"
cap vm_absent_firmware "ls /usr/local/share/uefi-firmware/"

$PRIV pkg install -y vm-bhyve edk2-bhyve >/dev/null || exit 1

# --- 1: installed, not enabled ---------------------------------------------
cap vm_installed_version "$PRIV vm version"
cap vm_installed_list "$PRIV vm list"
cap vm_installed_sysrc "sysrc -n vm_enable vm_dir"
cap vm_installed_firmware "ls /usr/local/share/uefi-firmware/"

# --- 2: enabled, no vm_dir --------------------------------------------------
$PRIV sysrc -q vm_enable=YES >/dev/null
cap vm_enabled_nodir_list "$PRIV vm list"
cap vm_enabled_nodir_sysrc "sysrc -n vm_enable vm_dir"

# --- 3: vm_dir naming a dataset that does not exist -------------------------
$PRIV sysrc -q vm_dir="zfs:$DS" >/dev/null
cap vm_dir_missing_list "$PRIV vm list"
cap vm_dir_missing_sysrc "sysrc -n vm_enable vm_dir"

# --- 4: the dataset exists, and is not mounted ------------------------------
# What a host whose pool root has mountpoint=none gets from a plain zfs create
# -- isengard's tank does. vm-bhyve says the same thing as for a dataset that
# does not exist at all.
$PRIV zfs create "$DS"
cap vm_unmounted_mountpoint "zfs get -H -o value mountpoint $DS"
cap vm_unmounted_list "$PRIV vm list"

# --- 5: mounted, before vm init ---------------------------------------------
$PRIV zfs set mountpoint="$MNT" "$DS"
cap vm_uninit_list "$PRIV vm list"
cap vm_uninit_datastore "$PRIV vm datastore list"

# --- 6: ready ----------------------------------------------------------------
cap vm_init "$PRIV vm init"
cap vm_ready_list "$PRIV vm list"
cap vm_ready_datastore "$PRIV vm datastore list"
cap vm_ready_sysrc "sysrc -n vm_enable vm_dir"
cap vm_ready_layout "ls -A $MNT"

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
