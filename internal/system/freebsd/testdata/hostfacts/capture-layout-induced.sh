#!/bin/sh
# Capture the jails dataset layout in the two states #21 has to handle and a provisioned
# host is not in: partly there, and present but mounted somewhere else. Also the bytes
# of zfs create itself, the write path no other capture has.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. On a host whose layout is complete it renames
# <pool>/jails/media aside (its contents are kept, unmounted), creates an empty
# replacement, moves that one's mountpoint, then destroys the replacement and renames the
# original back. Run it on a laboratory box, never on anything whose state matters. It
# restores on exit even if it is interrupted, and prints the layout before and after.
#
# Usage: ssh <host> 'sh -s [pool]' < capture-layout-induced.sh   (pool defaults to zroot)
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

POOL=${1:-zroot}
JAILS="$POOL/jails"
ASIDE="$POOL/jd-capture-media-aside"
MARK="jaildeck:capture" # set on the replacement, so restore destroys only what it made
OUT=/tmp/jd-capture-layout-induced
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

exists() { zfs list -H -o name "$1" >/dev/null 2>&1; }

# Refuse unless the layout is complete and nothing from an earlier run is left over:
# the restore below assumes exactly the state this script creates.
if ! exists "$JAILS/media" || ! exists "$JAILS/templates" || ! exists "$JAILS/containers"; then
	echo "$JAILS is not complete; this script starts from a provisioned host" >&2
	exit 1
fi
if exists "$ASIDE"; then
	echo "$ASIDE exists — an earlier run did not restore; fix that by hand first" >&2
	exit 1
fi
if [ "$(zfs get -H -o source mountpoint "$JAILS/media")" != "inherited from $JAILS" ]; then
	echo "$JAILS/media does not inherit its mountpoint; refusing to rename it" >&2
	exit 1
fi

layout() { zfs list -H -o name,mountpoint -r "$JAILS" "$ASIDE" 2>/dev/null; }

restore() {
	trap - EXIT INT TERM
	echo "--- restoring"
	if exists "$ASIDE"; then
		if exists "$JAILS/media" && [ "$(zfs get -H -o value "$MARK" "$JAILS/media")" = "1" ]; then
			$PRIV zfs destroy "$JAILS/media"
		fi
		$PRIV zfs rename "$ASIDE" "$JAILS/media" && $PRIV zfs inherit mountpoint "$JAILS/media"
	fi
	$PRIV rmdir /mnt/jd-capture-misplaced 2>/dev/null
	layout
}
trap restore EXIT INT TERM

echo "--- before"
layout

# --- partly there: the parent and two children, media missing ------------------
$PRIV zfs rename "$JAILS/media" "$ASIDE" || exit 1
cap layout_partial_zfs_list "zfs list -H -o name,mountpoint -r $POOL"

# --- zfs create: a child, created bare as #21 does, then again when it exists ---
cap layout_zfs_create "$PRIV zfs create $JAILS/media"
$PRIV zfs set "$MARK=1" "$JAILS/media"
cap layout_zfs_create_exists "$PRIV zfs create $JAILS/media"

# --- present but mounted elsewhere: #21's 409 ----------------------------------
$PRIV zfs set mountpoint=/mnt/jd-capture-misplaced "$JAILS/media"
cap layout_misplaced_zfs_list "zfs list -H -o name,mountpoint -r $POOL"

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
