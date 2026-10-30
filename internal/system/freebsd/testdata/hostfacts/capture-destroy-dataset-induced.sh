#!/bin/sh
# Capture destroying one dataset with its snapshots, for #120: a jail's child,
# or a retained dataset, without the jail.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It makes a scratch tree,
# <pool>/jd-cap-120, shaped like Jail Deck's -- a jail's dataset with a child,
# and a retained dataset -- and destroys the tree on exit, even if interrupted.
# Nothing under <pool>/jails is touched, so snapshot and replication jobs that
# take <pool>/jails do not copy it. One jail runs, jd-cap-120, persistent and
# empty: rooted at the scratch jail's path with no process inside, so a child
# mounted in a running jail can be measured without running anything there.
#
# Usage: capture-destroy-dataset-induced.sh [pool]   (default: tank)
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

POOL=${1:-tank}
T=$POOL/jd-cap-120
M=/var/tmp/jd-cap-120
J=jd-cap-120
OUT=/tmp/jd-capture-destroy
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

zfs list -H -o name "$T" >/dev/null 2>&1 && { echo "capture-destroy-dataset-induced: $T exists; refusing." >&2; exit 1; }
jls -j "$J" >/dev/null 2>&1 && { echo "capture-destroy-dataset-induced: jail $J runs; refusing." >&2; exit 1; }

restore() {
	echo "--- restoring"
	[ -n "$HOLDER" ] && kill "$HOLDER" 2>/dev/null
	$PRIV jail -r "$J" >/dev/null 2>&1
	$PRIV zfs release -r jd-cap "$T/retained/kept@one" >/dev/null 2>&1
	$PRIV zfs destroy -R "$T" >/dev/null 2>&1 || $PRIV zfs destroy -r "$T"
	$PRIV rm -rf "$M"
	printf 'tree:     %s\n' "$(zfs list -H -o name "$T" 2>&1)"
	printf 'jail:     %s\n' "$(jls -j "$J" name 2>&1)"
	printf 'mounts:   %s\n' "$(mount | grep -c "$M")"
}
trap restore EXIT INT TERM

J1=$T/containers/j1
listing() { cap "$1" "zfs list -H -o name -t all -r $T"; }
cap des_setup "$PRIV zfs create -o mountpoint=$M $T && $PRIV zfs create $T/containers && $PRIV zfs create $J1 && \
	$PRIV zfs create $J1/data && $PRIV zfs create $T/retained && $PRIV zfs create $T/retained/kept"
listing des_before

# A child with snapshots: without -r zfs refuses and names them; with -r they go.
cap des_child_snap "$PRIV zfs snapshot $J1/data@one && $PRIV zfs snapshot $J1/data@two"
cap des_child_bare "$PRIV zfs destroy $J1/data"
cap des_child_dry "$PRIV zfs destroy -nvr $J1/data"
cap des_child_r "$PRIV zfs destroy -r $J1/data"
listing des_after_child
cap des_child_again "$PRIV zfs destroy -r $J1/data"

# A retained dataset whose snapshot is held, then one with a clone.
cap des_hold "$PRIV zfs snapshot $T/retained/kept@one && $PRIV zfs hold jd-cap $T/retained/kept@one"
cap des_held_r "$PRIV zfs destroy -r $T/retained/kept"
cap des_holds "zfs holds -H $T/retained/kept@one"
cap des_release "$PRIV zfs release jd-cap $T/retained/kept@one"
cap des_clone "$PRIV zfs clone $T/retained/kept@one $T/containers/from-kept"
cap des_cloned_r "$PRIV zfs destroy -r $T/retained/kept"
cap des_clones "zfs get -H -o value clones $T/retained/kept@one"
cap des_unclone "$PRIV zfs destroy $T/containers/from-kept"

# A retained dataset with a child of its own: -r takes it too, silently.
cap des_nested "$PRIV zfs create $T/retained/kept/inner"
cap des_nested_dry "$PRIV zfs destroy -nvr $T/retained/kept"
cap des_nested_bare "$PRIV zfs destroy $T/retained/kept"
cap des_nested_r "$PRIV zfs destroy -r $T/retained/kept"
listing des_after_retained

# A child mounted in a running jail with nothing in it.
cap des_run_child "$PRIV zfs create $J1/data && $PRIV sh -c 'echo kept > $M/containers/j1/data/marker'"
cap des_run_start "$PRIV jail -c name=$J path=$M/containers/j1 persist ip4=disable ip6=disable"
cap des_run_destroy "$PRIV zfs destroy -r $J1/data"
cap des_run_after "jls -j $J name; ls -A $M/containers/j1; mount | grep -c $M/containers/j1/data"
cap des_run_stop "$PRIV jail -r $J"

# A child whose files a host process has open: the mount is busy. -r takes
# the snapshots first and refuses at the unmount, so a refused destroy has
# already lost them; an unmount first refuses before anything is destroyed.
cap des_busy_child "$PRIV zfs create $J1/data && $PRIV zfs snapshot $J1/data@one"
(cd "$M/containers/j1/data" && exec sleep 60) &
HOLDER=$!
sleep 1
cap des_busy_destroy "$PRIV zfs destroy -r $J1/data"
listing des_busy_destroy_left
kill "$HOLDER"; wait "$HOLDER" 2>/dev/null; HOLDER=
cap des_busy_after "$PRIV zfs destroy -r $J1/data"
cap des_busy2_child "$PRIV zfs create $J1/data && $PRIV zfs snapshot $J1/data@one"
(cd "$M/containers/j1/data" && exec sleep 60) &
HOLDER=$!
sleep 1
cap des_busy2_unmount "$PRIV zfs unmount $J1/data"
listing des_busy2_unmount_left
kill "$HOLDER"; wait "$HOLDER" 2>/dev/null; HOLDER=
cap des_busy2_unmount_free "$PRIV zfs unmount $J1/data"
cap des_busy2_destroy "$PRIV zfs destroy -r $J1/data"
listing des_end

echo "--- done: $(wc -l < "$OUT/MANIFEST") commands in $OUT"
