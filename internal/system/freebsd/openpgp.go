package freebsd

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math/big"
	"strings"
)

// Checking a publisher's OpenPGP signature against a key Jail Deck carries
// (JD-054). This is not an OpenPGP implementation: there is no keyring, no
// trust, no expiry and no revocation, because the key is pinned in the binary
// by its fingerprint and a new key arrives in a new release. What is read is
// what the publishers in the catalog use, and anything else is refused by
// name: v4 signatures of a binary document or a canonical text, by RSA or
// Ed25519, over SHA-256 or SHA-512. The arithmetic is the standard library's.

//go:embed keys/*.asc
var publisherKeys embed.FS

// keyFiles is where signingKey.file is read from: the keys built in, or a
// test's own.
var keyFiles fs.FS = mustSub(publisherKeys, "keys")

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// signingKey is a publisher's key: the file it is kept in, under keys/, and
// the v4 fingerprint of the key or subkey in it that signs. The fingerprint
// is computed from the file, so a key file and the fingerprint written here
// cannot disagree unnoticed.
type signingKey struct {
	file        string
	fingerprint string
	// name is who the key belongs to, for the error that says it did not
	// sign.
	name string
}

// OpenPGP's numbers for what is read.
const (
	pgpTagSignature    = 2
	pgpTagPublicKey    = 6
	pgpTagPublicSubkey = 14

	pgpSigBinary = 0x00
	pgpSigText   = 0x01

	pgpRSA     = 1
	pgpRSASign = 3
	pgpEdDSA   = 22
	pgpSHA256  = 8
	pgpSHA512  = 10
)

// The OID of Ed25519 in the legacy EdDSA encoding, 1.3.6.1.4.1.11591.15.1.
var oidEd25519 = []byte{0x2b, 0x06, 0x01, 0x04, 0x01, 0xda, 0x47, 0x0f, 0x01}

type pgpPacket struct {
	tag  byte
	body []byte
}

// pgpPackets splits binary OpenPGP data into packets, old and new format
// headers both. Partial lengths, which only streamed data uses, are refused.
func pgpPackets(data []byte) ([]pgpPacket, error) {
	var out []pgpPacket
	for len(data) > 0 {
		head := data[0]
		if head&0x80 == 0 {
			return nil, errors.New("not OpenPGP data")
		}
		var tag byte
		var n, skip int
		if head&0x40 != 0 { // new format
			tag = head & 0x3f
			if len(data) < 2 {
				return nil, errors.New("truncated OpenPGP packet")
			}
			switch first := int(data[1]); {
			case first < 192:
				n, skip = first, 2
			case first < 224:
				if len(data) < 3 {
					return nil, errors.New("truncated OpenPGP packet")
				}
				n, skip = (first-192)<<8+int(data[2])+192, 3
			case first == 255:
				if len(data) < 6 {
					return nil, errors.New("truncated OpenPGP packet")
				}
				n, skip = int(binary.BigEndian.Uint32(data[2:6])), 6
			default:
				return nil, errors.New("an OpenPGP packet with a partial length")
			}
		} else { // old format
			tag = head >> 2 & 0x0f
			switch head & 3 {
			case 0:
				if len(data) < 2 {
					return nil, errors.New("truncated OpenPGP packet")
				}
				n, skip = int(data[1]), 2
			case 1:
				if len(data) < 3 {
					return nil, errors.New("truncated OpenPGP packet")
				}
				n, skip = int(binary.BigEndian.Uint16(data[1:3])), 3
			case 2:
				if len(data) < 5 {
					return nil, errors.New("truncated OpenPGP packet")
				}
				n, skip = int(binary.BigEndian.Uint32(data[1:5])), 5
			default:
				return nil, errors.New("an OpenPGP packet of indeterminate length")
			}
		}
		if n < 0 || len(data) < skip+n {
			return nil, errors.New("truncated OpenPGP packet")
		}
		out = append(out, pgpPacket{tag: tag, body: data[skip : skip+n]})
		data = data[skip+n:]
	}
	return out, nil
}

// pgpDearmor returns the binary form of ASCII-armored data, and binary data
// as it is. The checksum line is not needed: the signature itself fails on a
// changed byte.
func pgpDearmor(data []byte) ([]byte, error) {
	text := strings.TrimLeft(string(data), " \t\r\n")
	if !strings.HasPrefix(text, "-----BEGIN PGP ") {
		return data, nil
	}
	var body strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Scan() // the BEGIN line
	inHeaders := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "-----END PGP ") {
			return base64.StdEncoding.DecodeString(body.String())
		}
		if inHeaders {
			if line == "" {
				inHeaders = false
			} else if !strings.Contains(line, ": ") {
				// No headers at all: this line is already the body.
				inHeaders = false
				body.WriteString(line)
			}
			continue
		}
		if strings.HasPrefix(line, "=") {
			continue // the CRC-24 line
		}
		body.WriteString(line)
	}
	return nil, errors.New("armored OpenPGP data without its END line")
}

// mpi reads a multiprecision integer: a bit count, then the bytes.
func mpi(b []byte) (value, rest []byte, err error) {
	if len(b) < 2 {
		return nil, nil, errors.New("truncated OpenPGP number")
	}
	n := (int(binary.BigEndian.Uint16(b)) + 7) / 8
	if len(b) < 2+n {
		return nil, nil, errors.New("truncated OpenPGP number")
	}
	return b[2 : 2+n], b[2+n:], nil
}

type pgpPublicKey struct {
	rsa     *rsa.PublicKey
	ed25519 ed25519.PublicKey
}

// loadSigningKey reads a key file under keys/ and returns the key or subkey
// in it whose fingerprint is k's.
func loadSigningKey(k signingKey) (pgpPublicKey, error) {
	raw, err := fs.ReadFile(keyFiles, k.file)
	if err != nil {
		return pgpPublicKey{}, err
	}
	data, err := pgpDearmor(raw)
	if err != nil {
		return pgpPublicKey{}, fmt.Errorf("%s: %w", k.file, err)
	}
	packets, err := pgpPackets(data)
	if err != nil {
		return pgpPublicKey{}, fmt.Errorf("%s: %w", k.file, err)
	}
	for _, p := range packets {
		if p.tag != pgpTagPublicKey && p.tag != pgpTagPublicSubkey {
			continue
		}
		if len(p.body) > 0xffff {
			continue
		}
		fp := sha1.New()
		fp.Write([]byte{0x99, byte(len(p.body) >> 8), byte(len(p.body))})
		fp.Write(p.body)
		if !strings.EqualFold(hex.EncodeToString(fp.Sum(nil)), k.fingerprint) {
			continue
		}
		return parsePublicKey(p.body)
	}
	return pgpPublicKey{}, fmt.Errorf("%s holds no key with the fingerprint %s", k.file, k.fingerprint)
}

func parsePublicKey(body []byte) (pgpPublicKey, error) {
	if len(body) < 6 || body[0] != 4 {
		return pgpPublicKey{}, errors.New("not a v4 OpenPGP key")
	}
	algo, rest := body[5], body[6:]
	switch algo {
	case pgpRSA, pgpRSASign:
		n, rest, err := mpi(rest)
		if err != nil {
			return pgpPublicKey{}, err
		}
		e, _, err := mpi(rest)
		if err != nil {
			return pgpPublicKey{}, err
		}
		exponent := new(big.Int).SetBytes(e)
		if !exponent.IsInt64() || exponent.Int64() > 1<<31-1 {
			return pgpPublicKey{}, errors.New("an RSA key with an unusable exponent")
		}
		return pgpPublicKey{rsa: &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent.Int64())}}, nil
	case pgpEdDSA:
		if len(rest) < 1 || len(rest) < 1+int(rest[0]) || !bytes.Equal(rest[1:1+int(rest[0])], oidEd25519) {
			return pgpPublicKey{}, errors.New("an EdDSA key on a curve other than Ed25519")
		}
		point, _, err := mpi(rest[1+int(rest[0]):])
		if err != nil {
			return pgpPublicKey{}, err
		}
		// The point is prefixed with 0x40, the native encoding.
		if len(point) != 1+ed25519.PublicKeySize || point[0] != 0x40 {
			return pgpPublicKey{}, errors.New("an Ed25519 key in an unknown encoding")
		}
		return pgpPublicKey{ed25519: ed25519.PublicKey(point[1:])}, nil
	}
	return pgpPublicKey{}, fmt.Errorf("an OpenPGP key of algorithm %d, which Jail Deck does not read", algo)
}

// pgpSignature is a v4 signature packet, read.
type pgpSignature struct {
	sigType byte
	algo    byte
	hash    crypto.Hash
	// hashed is the part of the packet the hash covers: version through the
	// hashed subpackets.
	hashed []byte
	left16 []byte
	values [][]byte
}

// readSignature returns the one signature in sig, armored or binary.
func readSignature(sig []byte) (pgpSignature, error) {
	data, err := pgpDearmor(sig)
	if err != nil {
		return pgpSignature{}, err
	}
	packets, err := pgpPackets(data)
	if err != nil {
		return pgpSignature{}, err
	}
	var found []pgpSignature
	for _, p := range packets {
		if p.tag != pgpTagSignature {
			continue
		}
		s, err := parseSignature(p.body)
		if err != nil {
			return pgpSignature{}, err
		}
		found = append(found, s)
	}
	if len(found) != 1 {
		return pgpSignature{}, fmt.Errorf("%d OpenPGP signatures where one was expected", len(found))
	}
	return found[0], nil
}

func parseSignature(b []byte) (pgpSignature, error) {
	if len(b) < 6 || b[0] != 4 {
		return pgpSignature{}, errors.New("not a v4 OpenPGP signature")
	}
	s := pgpSignature{sigType: b[1], algo: b[2]}
	switch b[3] {
	case pgpSHA256:
		s.hash = crypto.SHA256
	case pgpSHA512:
		s.hash = crypto.SHA512
	default:
		return pgpSignature{}, fmt.Errorf("an OpenPGP signature over hash %d, which Jail Deck does not read", b[3])
	}
	hashedLen := int(binary.BigEndian.Uint16(b[4:6]))
	if len(b) < 6+hashedLen+2 {
		return pgpSignature{}, errors.New("truncated OpenPGP signature")
	}
	s.hashed = b[:6+hashedLen]
	rest := b[6+hashedLen:]
	unhashedLen := int(binary.BigEndian.Uint16(rest))
	if len(rest) < 2+unhashedLen+2 {
		return pgpSignature{}, errors.New("truncated OpenPGP signature")
	}
	rest = rest[2+unhashedLen:]
	s.left16, rest = rest[:2], rest[2:]
	count := map[byte]int{pgpRSA: 1, pgpRSASign: 1, pgpEdDSA: 2}[s.algo]
	if count == 0 {
		return pgpSignature{}, fmt.Errorf("an OpenPGP signature of algorithm %d, which Jail Deck does not read", s.algo)
	}
	for range count {
		v, r, err := mpi(rest)
		if err != nil {
			return pgpSignature{}, err
		}
		s.values, rest = append(s.values, v), r
	}
	return s, nil
}

// newHash starts the digest of a signed document: the document is written
// into it, then finish adds the signature's own trailer.
func (s pgpSignature) newHash() hash.Hash {
	if s.hash == crypto.SHA256 {
		return sha256.New()
	}
	return sha512.New()
}

// check finishes h, which has had the document written into it, and checks
// the signature against key.
func (s pgpSignature) check(h hash.Hash, key pgpPublicKey) error {
	h.Write(s.hashed)
	trailer := []byte{4, 0xff, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(trailer[2:], uint32(len(s.hashed)))
	h.Write(trailer)
	digest := h.Sum(nil)
	if !bytes.Equal(digest[:2], s.left16) {
		return errBadSignature
	}
	switch {
	case key.rsa != nil && (s.algo == pgpRSA || s.algo == pgpRSASign):
		// The signature is as long as the modulus; leading zeros are not
		// carried in the number.
		size := (key.rsa.N.BitLen() + 7) / 8
		if len(s.values[0]) > size {
			return errBadSignature
		}
		sig := make([]byte, size)
		copy(sig[size-len(s.values[0]):], s.values[0])
		if rsa.VerifyPKCS1v15(key.rsa, s.hash, digest, sig) != nil {
			return errBadSignature
		}
		return nil
	case key.ed25519 != nil && s.algo == pgpEdDSA:
		r, sv := s.values[0], s.values[1]
		if len(r) > 32 || len(sv) > 32 {
			return errBadSignature
		}
		sig := make([]byte, ed25519.SignatureSize)
		copy(sig[32-len(r):32], r)
		copy(sig[64-len(sv):], sv)
		if !ed25519.Verify(key.ed25519, digest, sig) {
			return errBadSignature
		}
		return nil
	}
	return errors.New("the signature is not of the publisher key's kind")
}

var errBadSignature = errors.New("the signature does not match")

// verifyDetached checks a detached signature over a binary document read
// from r.
func verifyDetached(k signingKey, r io.Reader, sig []byte) error {
	key, err := loadSigningKey(k)
	if err != nil {
		return err
	}
	s, err := readSignature(sig)
	if err != nil {
		return err
	}
	if s.sigType != pgpSigBinary {
		return fmt.Errorf("a signature of type %#x where a binary document's was expected", s.sigType)
	}
	h := s.newHash()
	if _, err := io.Copy(h, r); err != nil {
		return err
	}
	return s.check(h, key)
}

// verifyCleartext checks a message signed in OpenPGP's cleartext form, as
// Fedora signs its checksum files, and returns the text that was signed. Only
// that text may be read afterwards: anything outside the signed part of the
// file is not vouched for.
func verifyCleartext(k signingKey, message []byte) (string, error) {
	text := strings.ReplaceAll(string(message), "\r\n", "\n")
	const begin, sigBegin = "-----BEGIN PGP SIGNED MESSAGE-----\n", "\n-----BEGIN PGP SIGNATURE-----"
	start := strings.Index(text, begin)
	if start < 0 {
		return "", errors.New("not an OpenPGP signed message")
	}
	text = text[start+len(begin):]
	// The armor headers ("Hash: SHA256") end at the first empty line.
	headersEnd := strings.Index(text, "\n\n")
	end := strings.Index(text, sigBegin)
	if headersEnd < 0 || end < headersEnd {
		return "", errors.New("an OpenPGP signed message without its signature")
	}
	body, sig := text[headersEnd+2:end], text[end+1:]
	if strings.Contains(sig[len("-----BEGIN PGP SIGNATURE-----"):], "-----BEGIN PGP SIGNED MESSAGE-----") {
		return "", errors.New("more than one OpenPGP signed message")
	}

	key, err := loadSigningKey(k)
	if err != nil {
		return "", err
	}
	s, err := readSignature([]byte(sig))
	if err != nil {
		return "", err
	}
	if s.sigType != pgpSigText {
		return "", fmt.Errorf("a signature of type %#x where a text's was expected", s.sigType)
	}
	// What is signed is the text with its dash escapes removed, trailing
	// spaces and tabs dropped from each line, and lines joined by CRLF, with
	// no line ending after the last (RFC 4880 7.1).
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		line = strings.TrimPrefix(line, "- ")
		lines[i] = strings.TrimRight(line, " \t")
	}
	h := s.newHash()
	h.Write([]byte(strings.Join(lines, "\r\n")))
	if err := s.check(h, key); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}
