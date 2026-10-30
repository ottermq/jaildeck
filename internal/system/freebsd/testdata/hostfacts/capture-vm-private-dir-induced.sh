#!/bin/sh
# Capture whether a guest's directory can be made private to root, and stay
# so, for a guest whose seed carries a console password (#152).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It needs vm-bhyve configured. It
# creates one throwaway guest, jd-cap-priv, on an empty sparse disk with no
# network and no image, from a user-data file carrying a placeholder hash in
# bcrypt's shape, as Jail Deck writes one (vm create -u). It starts it, stops
# it, makes its directory 0700, starts and stops it twice more, and destroys
# it and the template again. Run it on a laboratory box, as a user that is not
# root: what that user can read is the point.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit. The
# MANIFEST adds how long each one took.

OUT=/tmp/jd-capture-vm-private-dir
WORK=/var/tmp/jd-capture-vm-private-dir
rm -rf "$OUT"
mkdir -p "$OUT"
: > "$OUT/MANIFEST"

PRIV=""
if [ "$(id -u)" -ne 0 ]; then
	if command -v doas >/dev/null 2>&1; then PRIV="doas"
	elif command -v sudo >/dev/null 2>&1; then PRIV="sudo -n"; fi
fi
[ "$(id -u)" -eq 0 ] && { echo "capture-vm-private-dir-induced: run it as a user that is not root." >&2; exit 1; }

cap() {
	name=$1
	shift
	t0=$(date +%s)
	sh -c "$*" >"$OUT/$name.stdout" 2>"$OUT/$name.stderr"
	status=$?
	echo "$status" >"$OUT/$name.exit"
	printf '%s\t%s\texit=%s\t%ss\n' "$name" "$*" "$status" "$(( $(date +%s) - t0 ))" >>"$OUT/MANIFEST"
	return $status
}

if ! $PRIV vm list >/dev/null 2>&1; then
	echo "capture-vm-private-dir-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
g=jd-cap-priv
template="$store/.templates/jd-capture-priv.conf"
$PRIV vm list | grep -q '^jd-cap-' && { echo "capture-vm-private-dir-induced: jd-cap-* guests exist; refusing." >&2; exit 1; }
for p in "$template" "$WORK"; do
	[ -e "$p" ] && { echo "capture-vm-private-dir-induced: $p exists; refusing." >&2; exit 1; }
done
sys_before=$($PRIV sha256 -q "$store/.config/system.conf")

restore() {
	echo "--- restoring"
	$PRIV vm poweroff -f $g >/dev/null 2>&1
	sleep 2
	$PRIV vm destroy -f $g >/dev/null 2>&1
	$PRIV rm -f "$template"
	$PRIV rm -rf "$WORK"
	echo "guests left: $($PRIV vm list | grep -c '^jd-cap-')"
	echo "template: $([ -e "$template" ] && echo LEFT || echo removed)"
	echo "system.conf: $([ "$($PRIV sha256 -q "$store/.config/system.conf")" = "$sys_before" ] && echo as before || echo CHANGED)"
}
trap restore EXIT INT TERM

$PRIV mkdir -p "$WORK"
$PRIV sh -c "umask 077; cat > $WORK/user-data" <<'USERDATA'
#cloud-config
resize_rootfs: True
manage_etc_hosts: localhost
password: "$2a$10$jdcaptureplaceholderhashXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
chpasswd:
  expire: false
ssh_pwauth: false
USERDATA
$PRIV sh -c "cat > $template" <<'TEMPLATE'
loader="uefi"
cpu=1
memory=256M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE

# readable <capture>: what this user, not root, reads of the guest's files.
readable() {
	cap "$1" "for f in $store/$g/$g.conf $store/$g/seed.iso $store/$g/.cloud-init/user-data; do
		if grep -q -a . \"\$f\" 2>/dev/null; then echo \"readable \$f\"; else echo \"refused \$f\"; fi
	done; if grep -q -a jdcaptureplaceholder $store/$g/seed.iso 2>/dev/null; then echo 'hash read'; else echo 'hash not read'; fi"
}
mode() { cap "$1" "$PRIV stat -f '%Sp %Su:%Sg %N' $store/$g $store/$g/seed.iso 2>&1"; }

cap vpd_create "$PRIV vm create -t jd-capture-priv -s 1G -C -u $WORK/user-data $g" || exit 1
$PRIV vm start $g </dev/null >/dev/null 2>&1; sleep 5
cap vpd_poweroff_first "$PRIV vm poweroff -f $g"; sleep 3
mode vpd_mode_as_made
readable vpd_read_as_made

cap vpd_chmod "$PRIV chmod 0700 $store/$g"
mode vpd_mode_private
readable vpd_read_private

$PRIV vm start $g </dev/null >/dev/null 2>&1; sleep 5
cap vpd_list_running "$PRIV vm list"
mode vpd_mode_running
readable vpd_read_running
cap vpd_poweroff_second "$PRIV vm poweroff -f $g"; sleep 3
$PRIV vm start $g </dev/null >/dev/null 2>&1; sleep 5
cap vpd_list_restarted "$PRIV vm list"
cap vpd_poweroff_third "$PRIV vm poweroff -f $g"; sleep 3
mode vpd_mode_after_restarts
cap vpd_destroy "$PRIV vm destroy -f $g"
cap vpd_after_destroy "$PRIV vm list; ls -d $store/$g 2>&1"

echo "captured into $OUT"
