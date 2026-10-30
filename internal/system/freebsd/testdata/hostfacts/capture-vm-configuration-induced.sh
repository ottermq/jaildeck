#!/bin/sh
# Capture what a guest's directory holds on disk, and with what modes, for
# the read-only Configuration view (#127, JD-048).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It needs vm-bhyve configured. It
# creates two throwaway guests on an empty sparse disk with no network and no
# image: jd-cap-cfgu with a user-data file carrying a console password's hash,
# as Jail Deck writes one (vm create -u), and jd-cap-cfgk with an SSH key
# (vm create -k). The hash is a placeholder in bcrypt's shape, not anyone's
# password. It starts the first one, to see what its first start adds, stops
# it, and destroys both and the template again. Run it on a laboratory box.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit. The
# MANIFEST adds how long each one took.

OUT=/tmp/jd-capture-vm-configuration
WORK=/var/tmp/jd-capture-vm-configuration
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
	t0=$(date +%s)
	sh -c "$*" >"$OUT/$name.stdout" 2>"$OUT/$name.stderr"
	status=$?
	echo "$status" >"$OUT/$name.exit"
	printf '%s\t%s\texit=%s\t%ss\n' "$name" "$*" "$status" "$(( $(date +%s) - t0 ))" >>"$OUT/MANIFEST"
	return $status
}

if ! $PRIV vm list >/dev/null 2>&1; then
	echo "capture-vm-configuration-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
gu=jd-cap-cfgu
gk=jd-cap-cfgk
template="$store/.templates/jd-capture-cfg.conf"
$PRIV vm list | grep -q '^jd-cap-' && { echo "capture-vm-configuration-induced: jd-cap-* guests exist; refusing." >&2; exit 1; }
for p in "$template" "$WORK"; do
	[ -e "$p" ] && { echo "capture-vm-configuration-induced: $p exists; refusing." >&2; exit 1; }
done
sys_before=$($PRIV sha256 -q "$store/.config/system.conf")

restore() {
	echo "--- restoring"
	$PRIV vm poweroff -f $gu >/dev/null 2>&1
	sleep 2
	$PRIV vm destroy -f $gu >/dev/null 2>&1
	$PRIV vm destroy -f $gk >/dev/null 2>&1
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
ssh_authorized_keys:
  - "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMz6uwq6uCySEimJJLIKB1qaSuK3GpjZ0BSxasBIfDG jd-capture"
password: "$2a$10$jdcaptureplaceholderhashXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
chpasswd:
  expire: false
ssh_pwauth: false
USERDATA
$PRIV sh -c "umask 077; echo 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMz6uwq6uCySEimJJLIKB1qaSuK3GpjZ0BSxasBIfDG jd-capture' > $WORK/key.pub"
$PRIV sh -c "cat > $template" <<'TEMPLATE'
loader="uefi"
cpu=1
memory=256M
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE

cap vcf_user_data_scratch "$PRIV stat -f '%Sp %Su:%Sg %z %N' $WORK/user-data $WORK/key.pub"
cap vcf_create_u "$PRIV vm create -t jd-capture-cfg -s 1G -C -u $WORK/user-data -n 'ip=10.79.0.30/24;gateway4=10.79.0.1' $gu" || exit 1
cap vcf_create_k "$PRIV vm create -t jd-capture-cfg -s 1G -C -k $WORK/key.pub $gk" || exit 1
cap vcf_dir_u "$PRIV sh -c 'cd $store/$gu && stat -f \"%Sp %Su:%Sg %z %N\" . * .* .cloud-init/* 2>&1'"
cap vcf_dir_k "$PRIV sh -c 'cd $store/$gk && stat -f \"%Sp %Su:%Sg %z %N\" . * .* .cloud-init/* 2>&1'"
cap vcf_user_data_u "$PRIV cat $store/$gu/.cloud-init/user-data"
cap vcf_user_data_k "$PRIV cat $store/$gk/.cloud-init/user-data"
cap vcf_conf_u "$PRIV cat $store/$gu/$gu.conf"
cap vcf_store_modes "stat -f '%Sp %Su:%Sg %N' $store $store/.config $store/.templates"
cap vcf_read_as_user "cat $store/$gu/.cloud-init/user-data >/dev/null && echo readable || echo refused"

# The first start builds seed.iso from the seed's files.
$PRIV vm start $gu </dev/null >/dev/null 2>&1
sleep 5
cap vcf_dir_u_started "$PRIV sh -c 'cd $store/$gu && stat -f \"%Sp %Su:%Sg %z %N\" . * .* .cloud-init/* 2>&1'"
cap vcf_poweroff "$PRIV vm poweroff -f $gu"
sleep 3
cap vcf_dir_u_stopped "$PRIV sh -c 'cd $store/$gu && stat -f \"%Sp %Su:%Sg %z %N\" . * .* .cloud-init/* 2>&1'"
cap vcf_rctl_none "$PRIV rctl jail:jd-cap-nosuch; echo \"exit \$?\""

echo "captured into $OUT"
