#!/bin/sh
# Capture retaining a child dataset whose mountpoint is set locally, for #121.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It makes a scratch tree,
# <pool>/jd-cap-121, shaped like Jail Deck's: a jail's dataset, a child of it
# with a local mountpoint inside the jail's path, and a "retained" parent; it
# moves the child the way JD-025 does, then the way the fix would, and destroys
# the tree on exit, even if interrupted. Nothing under <pool>/jails is touched:
# the tree sits beside it, so snapshot and replication jobs that take
# <pool>/jails do not copy it. No jail runs; ZFS alone is what is measured.
#
# Usage: capture-retain-mountpoint-induced.sh [pool]   (default: tank)
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

POOL=${1:-tank}
T=$POOL/jd-cap-121
M=/var/tmp/jd-cap-121
OUT=/tmp/jd-capture-retain
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

zfs list -H -o name "$T" >/dev/null 2>&1 && { echo "capture-retain-mountpoint-induced: $T exists; refusing." >&2; exit 1; }

restore() {
	echo "--- restoring"
	$PRIV zfs destroy -r "$T"
	$PRIV rm -rf "$M"
	printf 'tree:     %s\n' "$(zfs list -H -o name "$T" 2>&1)"
	printf 'mounts:   %s\n' "$(mount | grep -c "$M")"
}
trap restore EXIT INT TERM

state() { # what the child says about itself, wherever it is now
	cap "$1" "zfs get -H -o name,property,value,source mountpoint,mounted,jaildeck:mountpoint $2; mount | grep $M | sort"
}

J1=$T/containers/j1
cap ret_setup "$PRIV zfs create -o mountpoint=$M $T && $PRIV zfs create $T/containers && $PRIV zfs create $J1 && $PRIV zfs create $T/retained && \
	$PRIV zfs create -o mountpoint=$M/containers/j1/var/db/pg $J1/pg && $PRIV sh -c 'echo kept > $M/containers/j1/var/db/pg/marker'"
state ret_before $J1/pg

# Today's retain (JD-025): rename -u, unmount, mount.
cap ret_rename "$PRIV zfs rename -u $J1/pg $T/retained/j1-pg"
cap ret_unmount "$PRIV zfs unmount $T/retained/j1-pg"
cap ret_mount "$PRIV zfs mount $T/retained/j1-pg"
state ret_after_today $T/retained/j1-pg
cap ret_destroy_jail_today "$PRIV zfs destroy -r $J1"
cap ret_today_listing "zfs list -H -o name -r $T"

# Put it back as it was, by hand, and retain it the way the fix would: after
# the unmount, record the local mountpoint and inherit, then mount.
cap ret_j1_again "zfs list -H -o name $J1 >/dev/null 2>&1 || $PRIV zfs create $J1"
cap ret_back "$PRIV zfs unmount $T/retained/j1-pg; $PRIV zfs rename -u $T/retained/j1-pg $J1/pg && $PRIV zfs mount $J1/pg"
state ret_back_state $J1/pg
cap ret_fix_rename "$PRIV zfs rename -u $J1/pg $T/retained/j1-pg"
cap ret_fix_unmount "$PRIV zfs unmount $T/retained/j1-pg"
cap ret_fix_record "$PRIV zfs set jaildeck:mountpoint=var/db/pg $T/retained/j1-pg"
cap ret_fix_inherit "$PRIV zfs inherit mountpoint $T/retained/j1-pg"
state ret_fix_inherited $T/retained/j1-pg
cap ret_fix_mount "zfs get -H -o value mounted $T/retained/j1-pg | grep -qx yes || $PRIV zfs mount $T/retained/j1-pg"
state ret_fix_after $T/retained/j1-pg
cap ret_fix_marker "cat $M/retained/j1-pg/marker"
cap ret_fix_destroy_jail "$PRIV zfs destroy -r $J1"

# Restore into another jail: the recorded path rebuilt under that jail's.
J2=$T/containers/j2
cap ret_j2 "$PRIV zfs create $J2"
cap ret_res_rename "$PRIV zfs rename -u $T/retained/j1-pg $J2/pg"
cap ret_res_unmount "$PRIV zfs unmount $J2/pg"
cap ret_res_set "$PRIV zfs set mountpoint=$M/containers/j2/var/db/pg $J2/pg"
state ret_res_set_state $J2/pg
cap ret_res_forget "$PRIV zfs inherit jaildeck:mountpoint $J2/pg"
cap ret_res_mount "zfs get -H -o value mounted $J2/pg | grep -qx yes || $PRIV zfs mount $J2/pg"
state ret_res_after $J2/pg
cap ret_res_marker "cat $M/containers/j2/var/db/pg/marker"

# zfs set mountpoint on a mounted dataset: does it move the mount itself?
cap ret_set_mounted "$PRIV zfs set mountpoint=$M/containers/j2/srv $J2/pg"
state ret_set_mounted_state $J2/pg
cap ret_inherit_mounted "$PRIV zfs inherit mountpoint $J2/pg"
state ret_inherit_mounted_state $J2/pg

for f in "$OUT"/MANIFEST; do cat "$f"; done
