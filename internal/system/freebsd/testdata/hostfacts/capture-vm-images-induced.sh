#!/bin/sh
# Capture an image template for VMs being made and used, for VM creation
# (JD-019).
#
# UNLIKE capture.sh, THIS CHANGES THE HOST AND DOWNLOADS ABOUT 220 MB. It needs
# vm-bhyve configured and answering. It fetches Debian 13's genericcloud image,
# checks it against Debian's SHA512SUMS, writes its disk into a zvol under the
# default datastore, snapshots it, and creates one throwaway guest,
# jd-cap-img, whose disk is a clone of that snapshot, and one more clone left
# to GEOM to show why the guest's is not. The guest has no network:
# its cloud-init seed only tells it to power off once it has booted. It is then
# started twice more and stopped, once while its OS is still booting and once
# after. Everything is destroyed again, and it prints what it restored. Run it
# on a laboratory box.
#
# Same three files per command as capture.sh: .stdout, .stderr, .exit. The
# MANIFEST adds how long each one took.

OUT=/tmp/jd-capture-vm-images
WORK=/var/tmp/jd-capture-vm-images
URL=https://cloud.debian.org/images/cloud/trixie/latest
FILE=debian-13-genericcloud-amd64.tar.xz
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
	echo "capture-vm-images-induced: vm-bhyve is not configured and answering here." >&2
	exit 1
fi
if $PRIV vm list | grep -q '^jd-cap-'; then
	echo "capture-vm-images-induced: jd-cap-* guests already exist; refusing." >&2
	exit 1
fi
store=$($PRIV vm datastore list | awk '$1 == "default" { print $3 }')
ds=$($PRIV vm datastore list | awk '$1 == "default" { print $4 }')
[ -n "$ds" ] || { echo "capture-vm-images-induced: the default datastore is not on ZFS." >&2; exit 1; }
template="$store/.templates/jd-capture.conf"
images="$ds/.images"
image="$images/jd-cap-debian13"
g=jd-cap-img
for p in "$template" "$WORK"; do
	[ -e "$p" ] && { echo "capture-vm-images-induced: $p exists; refusing." >&2; exit 1; }
done
zfs list "$images" >/dev/null 2>&1 && { echo "capture-vm-images-induced: $images exists; refusing." >&2; exit 1; }
rc_before=$($PRIV sha256 -q /etc/rc.conf)
datasets_before=$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')

restore() {
	echo "--- restoring"
	$PRIV vm poweroff -f $g >/dev/null 2>&1
	s=$(date +%s)
	while [ -e /dev/vmm/$g ] && [ $(( $(date +%s) - s )) -lt 30 ]; do sleep 1; done
	$PRIV pkill -f "cat /dev/nmdm-$g" >/dev/null 2>&1
	$PRIV vm destroy -f $g >/dev/null 2>&1
	$PRIV gpart undo "zvol/$ds/jd-cap-geom" >/dev/null 2>&1
	$PRIV zfs destroy "$ds/jd-cap-geom" >/dev/null 2>&1
	$PRIV zfs destroy -r "$images" >/dev/null 2>&1
	$PRIV rm -f "$template"
	rm -rf "$WORK"
	printf 'guests:   %s\n' "$($PRIV vm list | awk 'NR > 1 { print $1 }' | tr '\n' ' ')"
	printf 'vmm:      %s\n' "$(ls /dev/vmm 2>/dev/null | tr '\n' ' ')"
	printf 'vm-run:   %s\n' "$(pgrep -fl "vm _run $g" | grep -v pgrep | wc -l | tr -d ' ')"
	printf 'template: %s\n' "$([ -e "$template" ] && echo LEFT || echo removed)"
	printf 'download: %s\n' "$([ -e "$WORK" ] && echo LEFT || echo removed)"
	printf 'datasets: %s\n' "$([ "$(zfs list -H -o name -r "$ds" | sort | tr '\n' ' ')" = "$datasets_before" ] && echo as-before || echo CHANGED)"
	printf 'rc.conf:  %s\n' "$([ "$($PRIV sha256 -q /etc/rc.conf)" = "$rc_before" ] && echo byte-identical || echo CHANGED)"
}
trap restore EXIT INT TERM

# --- the image: fetched, checked, listed --------------------------------------

mkdir -p "$WORK"
cap vm_img_fetch_sums "fetch -o $WORK/SHA512SUMS $URL/SHA512SUMS"
cap vm_img_fetch "fetch -o $WORK/$FILE $URL/$FILE" || exit 1
cap vm_img_sums_line "grep ' $FILE\$' $WORK/SHA512SUMS"
cap vm_img_sha512 "sha512 -q $WORK/$FILE"
[ "$(cut -d' ' -f1 "$OUT/vm_img_sums_line.stdout")" = "$(cat "$OUT/vm_img_sha512.stdout")" ] || {
	echo "capture-vm-images-induced: the image does not match SHA512SUMS." >&2; exit 1; }
cap vm_img_tar_list "tar -tvf $WORK/$FILE"
size=$(awk '$NF == "disk.raw" { print $5 }' "$OUT/vm_img_tar_list.stdout")
[ -n "$size" ] || { echo "capture-vm-images-induced: no disk.raw in the image." >&2; exit 1; }

# --- the image template: a zvol under the datastore, snapshotted --------------

# volmode=dev, inherited by every image under it: the host's GEOM does not
# taste a guest's partition table. The clone below shows why it matters.
cap vm_img_create_parent "$PRIV zfs create -o volmode=dev $images"
cap vm_img_vm_list_with_parent "$PRIV vm list"
cap vm_img_create_zvol "$PRIV zfs create -s -V $size $image"
cap vm_img_write "tar -xOf $WORK/$FILE disk.raw | $PRIV dd of=/dev/zvol/$image bs=1m"
cap vm_img_zvol_props "zfs get -H -p volsize,used,referenced,logicalreferenced,compression,volmode,refreservation $image"
cap vm_img_gpart "gpart show /dev/zvol/$image"
# LBA 1 is the GPT header; at byte 32 of it, this header's LBA and the backup
# header's, which is the disk's last sector when the table fills the disk.
gpt_header() { echo "dd if=/dev/zvol/$1 bs=512 skip=1 count=1 2>/dev/null | od -A d -t u8 -j 24 -N 16"; }
cap vm_img_gpt_header "$PRIV sh -c '$(gpt_header $image)'"
cap vm_img_snapshot "$PRIV zfs snapshot $image@base"
cap vm_img_vm_list_with_image "$PRIV vm list"

# --- a guest whose disk is a clone of it --------------------------------------

$PRIV sh -c "cat > '$template'" <<'TEMPLATE'
loader="uefi"
cpu=1
memory=1G
disk0_type="virtio-blk"
disk0_name="disk0"
disk0_dev="sparse-zvol"
TEMPLATE
cat > "$WORK/user-data" <<'USERDATA'
#cloud-config
power_state:
  mode: poweroff
  delay: now
  message: jd-capture powering off after first boot
USERDATA
cap vm_img_guest_create "$PRIV vm create -t jd-capture -s 8G -C -u $WORK/user-data $g" || exit 1
cap vm_img_guest_created "zfs list -H -p -o name,type,volsize,used,origin -r $ds/$g; ls -la $store/$g; $PRIV cat $store/$g/$g.conf"

# A clone in GEOM's default mode, grown past the image, first: GEOM_PART
# moves the backup GPT header to the new end by itself, and holds the disk
# open for writing until someone commits or undoes it. The guest's firmware
# then finds nothing to boot, and zfs destroy says the dataset is busy.
cap vm_img_geom_auto_resize "sysctl kern.geom.part.auto_resize"
cap vm_img_geom_clone "$PRIV zfs clone -o volsize=8G $image@base $ds/jd-cap-geom"
sleep 1
cap vm_img_geom_clone_held "geom part list zvol/$ds/jd-cap-geom | grep -E '^Consumers|Mode|modified'; dmesg | grep 'jd-cap-geom' | tail -2"
cap vm_img_geom_destroy_held "$PRIV zfs destroy $ds/jd-cap-geom"
cap vm_img_geom_undo "$PRIV gpart undo zvol/$ds/jd-cap-geom"
cap vm_img_geom_destroy "$PRIV zfs destroy $ds/jd-cap-geom"

# vm create made disk0 itself. It is replaced with the clone, in dev mode and
# at the guest's size from the start.
cap vm_img_disk_destroy "$PRIV zfs destroy $ds/$g/disk0"
cap vm_img_clone "$PRIV zfs clone -o volmode=dev -o volsize=8G $image@base $ds/$g/disk0" || exit 1
cap vm_img_clone_props "zfs get -H -p origin,volsize,used,referenced,refreservation,volmode $ds/$g/disk0"
cap vm_img_clone_gpt_before "$PRIV sh -c '$(gpt_header $ds/$g/disk0)'"
cap vm_img_guest_info "$PRIV vm info $g"

# The snapshot now has a dependant: what removing the template says.
cap vm_img_snapshot_clones "zfs get -H clones $image@base"
cap vm_img_destroy_snapshot_in_use "$PRIV zfs destroy $image@base"
cap vm_img_destroy_image_in_use "$PRIV zfs destroy $image"

# watch <capture> <seconds> [stop-when-stopped]: one line per second of vm
# list's row, the instance in /dev/vmm and the vm-run supervisor.
watch() {
	name=$1 n=$2 until_stopped=$3 i=0 seen=""
	t0=$(date +%s)
	: >"$OUT/$name.stdout"
	while [ $i -lt $n ]; do
		row=$($PRIV vm list | awk -v g=$g '$1 == g { $1 = ""; print }' | sed 's/^ *//')
		printf '%3ds  %s  vmm=%s  vm-run=%s\n' $i "$row" \
			"$([ -e /dev/vmm/$g ] && echo yes || echo no)" \
			"$(pgrep -f "vm _run $g" | grep -vx $$ | wc -l | tr -d ' ')" >>"$OUT/$name.stdout"
		case "$row" in *Running*) seen=yes ;; esac
		[ -n "$until_stopped" ] && [ -n "$seen" ] && case "$row" in *Stopped*) break ;; esac
		i=$((i + 1))
		sleep 1
	done
	echo 0 >"$OUT/$name.exit"; : >"$OUT/$name.stderr"
	printf '%s\t%s\texit=0\t%ss\n' "$name" "(state each second, up to ${n}s)" "$(( $(date +%s) - t0 ))" >>"$OUT/MANIFEST"
}

# The serial console, read from the host's end while the guest boots.
console() {
	s=$(date +%s)
	until [ -e /dev/nmdm-$g.1B ]; do
		[ $(( $(date +%s) - s )) -gt 30 ] && { ls /dev | grep nmdm >"$OUT/$2.stderr"; return; }
		sleep 1
	done
	$PRIV sh -c "timeout $1 cat /dev/nmdm-$g.1B" >"$OUT/$2.stdout" 2>"$OUT/$2.stderr" &
}

# First boot: cloud-init reads the seed, grows the root partition into the
# clone's new size, and powers the guest off. With no network interface,
# systemd waits for one first, so this takes minutes rather than seconds.
$PRIV vm start $g </dev/null >/dev/null 2>&1
console 240 vm_img_boot1_console
watch vm_img_boot1 240 until-stopped
sleep 2
cap vm_img_clone_gpt_after "$PRIV sh -c '$(gpt_header $ds/$g/disk0)'"
cap vm_img_clone_props_after "zfs get -H -p volsize,used,referenced $ds/$g/disk0"
cap vm_img_boot1_log "$PRIV cat $store/$g/vm-bhyve.log"

# Stopped part way into a boot, before the OS is up: JD-019 measured the
# power button ignored then, and JD-036's stop falls back to poweroff for it.
# Three seconds in is the firmware or the boot loader, ten is the kernel and
# systemd starting.
for at in 3 10; do
	$PRIV vm start $g </dev/null >/dev/null 2>&1
	until [ -e /dev/vmm/$g ]; do :; done
	sleep $at
	cap vm_img_stop_booting_${at}s "$PRIV vm stop $g"
	watch vm_img_after_stop_booting_${at}s 70 until-stopped
	$PRIV vm poweroff -f $g >/dev/null 2>&1
	while [ -e /dev/vmm/$g ]; do sleep 1; done
done

# Stopped once it has been up for a minute.
$PRIV vm start $g </dev/null >/dev/null 2>&1
until [ -e /dev/vmm/$g ]; do :; done
sleep 60
cap vm_img_stop_booted "$PRIV vm stop $g"
watch vm_img_after_stop_booted 70 until-stopped
$PRIV vm poweroff -f $g >/dev/null 2>&1
while [ -e /dev/vmm/$g ]; do sleep 1; done

# Destroying the guest takes its clone with it, and frees the snapshot.
cap vm_img_guest_destroy "$PRIV vm destroy -f $g"
cap vm_img_after_guest_destroy "zfs list -H -o name -r $ds; zfs get -H clones $image@base"

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
