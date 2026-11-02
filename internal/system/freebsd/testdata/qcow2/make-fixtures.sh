#!/bin/sh
# Makes the qcow2 fixtures the reader is tested against (#137), with qemu-img
# from QEMU, which Jail Deck itself never needs. Run from this directory; the
# outputs are committed, so this runs only when they change.
#
# disk.raw is 10 clusters of 4 KiB and 512 bytes more: data, a hole, a
# compressible run, holes, data again, and a last cluster cut short, which
# is the shape every published image's end is not but a reader must handle.
set -eu
command -v qemu-img >/dev/null || { echo "qemu-img is needed"; exit 1; }
rm -f disk.raw *.qcow2
k=4096
seeded() { openssl enc -aes-128-ctr -nosalt -pass pass:"$1" < /dev/zero 2>/dev/null | head -c "$2"; }
{
	seeded a $k                      # 0: data
	head -c $k /dev/zero             # 1: a hole
	yes 'jaildeck qcow2 fixture' | head -c $((2 * k))   # 2-3: compressible
	head -c $((4 * k)) /dev/zero     # 4-7: holes
	seeded b $k                      # 8: data
	yes 'last full cluster' | head -c $k              # 9
	seeded c 512                     # 10: cut short
} > disk.raw

qemu-img convert -q -f raw -O qcow2 -o cluster_size=4096 disk.raw plain.qcow2
qemu-img convert -q -f raw -O qcow2 -o cluster_size=4096 -c disk.raw compressed.qcow2
qemu-img convert -q -f raw -O qcow2 -o cluster_size=4096,compat=0.10 disk.raw v2.qcow2
qemu-img convert -q -f raw -O qcow2 -c disk.raw compressed-64k.qcow2
# Cluster 0 turned into a v3 zero cluster: the flag, not a hole.
cp plain.qcow2 zero-flag.qcow2
qemu-io -f qcow2 -c 'write -z 0 4k' zero-flag.qcow2 >/dev/null
sha256sum disk.raw > disk.raw.sha256
