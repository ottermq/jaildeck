#!/bin/sh
# Capture what Jail Deck reads of VM images, with the exact commands the
# adapter runs, for VM image templates (JD-037).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST, though it downloads nothing. It
# needs vm-bhyve configured on a ZFS datastore with no .images dataset yet. It
# creates .images, two small zvols standing in for images -- one sealed with
# @base and cloned for a guest's disk, one never sealed -- reads them as the
# adapter does, and destroys all of it. capture-vm-images-induced.sh is the one
# with a real image.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit.

OUT=/tmp/jd-capture-vm-image-list
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

ds=$($PRIV vm datastore list 2>/dev/null | awk '$1 == "default" { print $4 }')
[ -n "$ds" ] && [ "$ds" != "-" ] || { echo "capture-vm-image-list-induced: no default datastore on ZFS." >&2; exit 1; }
images="$ds/.images"
zfs list "$images" >/dev/null 2>&1 && { echo "capture-vm-image-list-induced: $images exists; refusing." >&2; exit 1; }
zfs list "$ds/jd-cap-g" >/dev/null 2>&1 && { echo "capture-vm-image-list-induced: $ds/jd-cap-g exists; refusing." >&2; exit 1; }
datasets_before=$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')

restore() {
	echo "--- restoring"
	$PRIV zfs destroy -r "$ds/jd-cap-g" >/dev/null 2>&1
	$PRIV zfs destroy -r "$images" >/dev/null 2>&1
	printf 'datasets: %s\n' "$([ "$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')" = "$datasets_before" ] && echo as-before || echo CHANGED)"
}
trap restore EXIT INT TERM

cols=name,volsize,used,jaildeck:source,jaildeck:sha512,clones
sum=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

# Before anything: no .images at all.
cap vm_imgls_absent "zfs list -H -p -t volume,snapshot -o $cols -r $images"
cap vm_imgls_dataset_absent "zfs list -H -o name $images"

cap vm_imgls_create_dataset "$PRIV zfs create -o volmode=dev $images"
cap vm_imgls_dataset_present "zfs list -H -o name $images"
cap vm_imgls_empty "zfs list -H -p -t volume,snapshot -o $cols -r $images"

# One image sealed and cloned for a guest's disk, with a second snapshot
# that is not @base; one written and never sealed.
cap vm_imgls_create_ready "$PRIV zfs create -s -V 64m -o volmode=dev -o jaildeck:source=debian-13 -o jaildeck:sha512=$sum $images/jd-cap-ready"
cap vm_imgls_seal "$PRIV zfs snapshot $images/jd-cap-ready@base"
$PRIV zfs snapshot "$images/jd-cap-ready@other"
$PRIV zfs create "$ds/jd-cap-g"
cap vm_imgls_clone "$PRIV zfs clone -o volmode=dev -o volsize=128m $images/jd-cap-ready@base $ds/jd-cap-g/disk0"
cap vm_imgls_create_written "$PRIV zfs create -s -V 64m -o volmode=dev -o jaildeck:source=debian-13 -o jaildeck:sha512=$sum $images/jd-cap-written"

cap vm_imgls_list "zfs list -H -p -t volume,snapshot -o $cols -r $images"
cap vm_imgls_vm_list "$PRIV vm list"

# Removing the image its guest's disk is cloned from, as the adapter would:
# zfs refuses.
cap vm_imgls_destroy_in_use "$PRIV zfs destroy -r $images/jd-cap-ready"
cap vm_imgls_destroy_written "$PRIV zfs destroy -r $images/jd-cap-written"

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
