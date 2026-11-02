package freebsd

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// A qcow2 reader, enough to write a cloud image's disk into a zvol (#137,
// JD-053). Ubuntu, Fedora, Alpine and Arch publish their cloud images as
// qcow2 and nothing in base reads it; qemu-img would, from a package with a
// large tree behind it. What the published images use was read off their
// headers first: version 3, 64 KiB clusters, no encryption, no backing file,
// no snapshots, no incompatible features, and so zlib for what is
// compressed. That is what this reads. Anything else is refused by name
// rather than written wrong.

const (
	qcow2Magic = 0x514649fb // "QFI\xfb"

	// An L1 or L2 entry's host offset, bits 9-55 (qcow2 spec).
	qcow2OffsetMask = 0x00fffffffffffe00
	// An L2 entry's flags.
	qcow2Compressed = 1 << 62
	qcow2ZeroFlag   = 1 // version 3: the cluster reads as zeros
)

type qcow2Image struct {
	file        io.ReaderAt
	size        int64
	clusterBits uint32
	l1          []uint64
}

// qcow2Header is the part of the header both versions share, in its order.
type qcow2Header struct {
	Magic                 uint32
	Version               uint32
	BackingFileOffset     uint64
	BackingFileSize       uint32
	ClusterBits           uint32
	Size                  uint64
	CryptMethod           uint32
	L1Size                uint32
	L1TableOffset         uint64
	RefcountTableOffset   uint64
	RefcountTableClusters uint32
	NbSnapshots           uint32
	SnapshotsOffset       uint64
}

// openQcow2 reads and checks the header and the L1 table.
func openQcow2(file io.ReaderAt) (*qcow2Image, error) {
	var h qcow2Header
	if err := binary.Read(io.NewSectionReader(file, 0, 72), binary.BigEndian, &h); err != nil {
		return nil, fmt.Errorf("qcow2 header: %w", err)
	}
	switch {
	case h.Magic != qcow2Magic:
		return nil, errors.New("not a qcow2 image")
	case h.Version != 2 && h.Version != 3:
		return nil, fmt.Errorf("qcow2 version %d is not one this reads", h.Version)
	case h.CryptMethod != 0:
		return nil, errors.New("an encrypted qcow2 image is not one this reads")
	case h.BackingFileOffset != 0:
		return nil, errors.New("a qcow2 image with a backing file is not one this reads")
	case h.ClusterBits < 9 || h.ClusterBits > 21:
		return nil, fmt.Errorf("qcow2 cluster_bits %d is out of range", h.ClusterBits)
	case h.Size == 0 || h.Size > 1<<44:
		return nil, fmt.Errorf("qcow2 virtual size %d is out of range", h.Size)
	}
	if h.Version == 3 {
		var incompatible uint64
		if err := binary.Read(io.NewSectionReader(file, 72, 8), binary.BigEndian, &incompatible); err != nil {
			return nil, fmt.Errorf("qcow2 header: %w", err)
		}
		// Dirty, corrupt, external data, a compression type other than zlib,
		// extended L2 entries: each changes what the tables mean.
		if incompatible != 0 {
			return nil, fmt.Errorf("qcow2 incompatible features %#x are not ones this reads", incompatible)
		}
	}
	q := &qcow2Image{file: file, size: int64(h.Size), clusterBits: h.ClusterBits}
	perL2 := int64(1) << (h.ClusterBits - 3)
	need := (q.size + q.clusterSize()*perL2 - 1) / (q.clusterSize() * perL2)
	if int64(h.L1Size) < need || h.L1Size > 1<<24 {
		return nil, fmt.Errorf("qcow2 L1 table of %d entries for a %d-byte disk", h.L1Size, h.Size)
	}
	raw := make([]byte, 8*int(h.L1Size))
	if _, err := file.ReadAt(raw, int64(h.L1TableOffset)); err != nil {
		return nil, fmt.Errorf("qcow2 L1 table: %w", err)
	}
	q.l1 = make([]uint64, h.L1Size)
	for i := range q.l1 {
		q.l1[i] = binary.BigEndian.Uint64(raw[8*i:])
	}
	return q, nil
}

func (q *qcow2Image) clusterSize() int64 { return 1 << q.clusterBits }

// writeTo writes every cluster that holds data at its offset in dst, which
// is a fresh sparse zvol: what is unallocated or zero is left as the zeros
// it already reads as. It returns the bytes written.
func (q *qcow2Image) writeTo(ctx context.Context, dst io.WriterAt) (int64, error) {
	cluster := q.clusterSize()
	perL2 := cluster / 8
	l2 := make([]byte, cluster)
	data := make([]byte, cluster)
	var written int64
	for i, entry := range q.l1 {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		table := int64(entry & qcow2OffsetMask)
		if table == 0 {
			continue
		}
		if _, err := q.file.ReadAt(l2, table); err != nil {
			return written, fmt.Errorf("qcow2 L2 table %d: %w", i, err)
		}
		for j := int64(0); j < perL2; j++ {
			guest := (int64(i)*perL2 + j) * cluster
			if guest >= q.size {
				break
			}
			e := binary.BigEndian.Uint64(l2[8*j:])
			length := min(cluster, q.size-guest)
			switch {
			case e&qcow2Compressed != 0:
				if err := q.readCompressed(e, data); err != nil {
					return written, fmt.Errorf("qcow2 cluster at %d: %w", guest, err)
				}
			case e&qcow2ZeroFlag != 0, e&qcow2OffsetMask == 0:
				continue
			default:
				if _, err := q.file.ReadAt(data[:length], int64(e&qcow2OffsetMask)); err != nil {
					return written, fmt.Errorf("qcow2 cluster at %d: %w", guest, err)
				}
			}
			if allZero(data[:length]) {
				continue
			}
			if _, err := dst.WriteAt(data[:length], guest); err != nil {
				return written, err
			}
			written += length
		}
	}
	return written, nil
}

// readCompressed inflates one compressed cluster into data, which is one
// cluster long. A compressed descriptor holds the host offset in its low
// 62-(cluster_bits-8) bits and, above them, how many more 512-byte sectors
// the compressed bytes run into; the data is raw deflate (qcow2 spec).
func (q *qcow2Image) readCompressed(e uint64, data []byte) error {
	shift := 62 - (q.clusterBits - 8)
	offset := int64(e & (1<<shift - 1))
	sectors := int64((e>>shift)&(1<<(q.clusterBits-8)-1)) + 1
	compressed := make([]byte, sectors*512-offset%512)
	n, err := q.file.ReadAt(compressed, offset)
	if err != nil && !(errors.Is(err, io.EOF) && n > 0) {
		return err
	}
	r := flate.NewReader(bytes.NewReader(compressed[:n]))
	defer r.Close()
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("inflating: %w", err)
	}
	return nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// qcow2Size reads a qcow2 file's virtual size: what its zvol is made.
func qcow2Size(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	q, err := openQcow2(f)
	if err != nil {
		return 0, err
	}
	return q.size, nil
}

// writeQcow2 writes a qcow2 file's disk into a device.
func writeQcow2(ctx context.Context, path, device string) (int64, error) {
	src, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	q, err := openQcow2(src)
	if err != nil {
		return 0, err
	}
	dst, err := os.OpenFile(device, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	written, err := q.writeTo(ctx, dst)
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	return written, err
}
