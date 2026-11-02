package freebsd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func pgpFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/openpgp/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testKeys points keyFiles at the constructed keys for one test.
func testKeys(t *testing.T) (ed, rsa signingKey) {
	t.Helper()
	old := keyFiles
	keyFiles = os.DirFS("testdata/openpgp")
	t.Cleanup(func() { keyFiles = old })
	fpr := func(k string) string { return strings.TrimSpace(string(pgpFixture(t, "test-"+k+".fpr"))) }
	return signingKey{file: "test-ed25519.asc", fingerprint: fpr("ed25519"), name: "the Ed25519 test key"},
		signingKey{file: "test-rsa.asc", fingerprint: fpr("rsa"), name: "the RSA test key"}
}

// Every key the catalog names is in the binary, under its fingerprint.
func TestEveryPublisherKeyIsBuiltIn(t *testing.T) {
	for _, entry := range imageCatalog {
		if entry.signed == "" {
			continue
		}
		if _, err := loadSigningKey(entry.key); err != nil {
			t.Errorf("%s: %v", entry.Name, err)
		}
		if (entry.signed == sigCleartext) != (entry.sig == "") {
			t.Errorf("%s: signed %q with signature file %q", entry.Name, entry.signed, entry.sig)
		}
	}
}

// The publishers' own files, as they were published.
func TestThePublishersSignaturesAreRead(t *testing.T) {
	sums := pgpFixture(t, "ubuntu-SHA256SUMS")
	if err := verifyDetached(ubuntuCloudKey, bytes.NewReader(sums), pgpFixture(t, "ubuntu-SHA256SUMS.gpg")); err != nil {
		t.Errorf("Ubuntu: %v", err)
	}
	text, err := verifyCleartext(fedora44Key, pgpFixture(t, "fedora-44-CHECKSUM"))
	if err != nil {
		t.Fatalf("Fedora: %v", err)
	}
	if got, err := sumsEntry(text, "Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2", "sha256"); err != nil || len(got) != 64 {
		t.Errorf("Fedora's signed text gives %q, %v", got, err)
	}

	// The wrong publisher's key is refused, and so is a changed byte.
	if err := verifyDetached(fedora44Key, bytes.NewReader(sums), pgpFixture(t, "ubuntu-SHA256SUMS.gpg")); err == nil {
		t.Error("Ubuntu's signature passed against Fedora's key")
	}
	changed := bytes.Replace(sums, []byte("a"), []byte("b"), 1)
	if err := verifyDetached(ubuntuCloudKey, bytes.NewReader(changed), pgpFixture(t, "ubuntu-SHA256SUMS.gpg")); err != errBadSignature {
		t.Errorf("a changed SHA256SUMS: %v", err)
	}
	fedora := pgpFixture(t, "fedora-44-CHECKSUM")
	i := bytes.Index(fedora, []byte("SHA256 ("))
	forged := append(append(append([]byte{}, fedora[:i]...), []byte("SHA256 (x) = "+strings.Repeat("0", 64)+"\n")...), fedora[i:]...)
	if _, err := verifyCleartext(fedora44Key, forged); err != errBadSignature {
		t.Errorf("a line added to Fedora's signed text: %v", err)
	}
}

func TestDetachedSignaturesInEachForm(t *testing.T) {
	ed, rsa := testKeys(t)
	archive := pgpFixture(t, "archive.bin")
	for name, c := range map[string]struct {
		key signingKey
		sig string
	}{
		"Ed25519, SHA-512, binary (Arch's form)": {ed, "archive.bin.sig"},
		"RSA, SHA-256, armored (Alpine's form)":  {rsa, "archive.bin.asc"},
	} {
		if err := verifyDetached(c.key, bytes.NewReader(archive), pgpFixture(t, c.sig)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if err := verifyDetached(c.key, strings.NewReader("another disk\n"), pgpFixture(t, c.sig)); err != errBadSignature {
			t.Errorf("%s, another document: %v", name, err)
		}
	}
	if err := verifyDetached(rsa, bytes.NewReader(archive), pgpFixture(t, "archive.bin.sig")); err == nil {
		t.Error("an Ed25519 signature passed against an RSA key")
	}
	if err := verifyDetached(ed, bytes.NewReader(archive), pgpFixture(t, "checksum.txt.asc")); err == nil {
		t.Error("a signed message was taken for a detached signature")
	}
	if err := verifyDetached(signingKey{file: "test-rsa.asc", fingerprint: strings.Repeat("0", 40)},
		bytes.NewReader(archive), pgpFixture(t, "archive.bin.asc")); err == nil || !strings.Contains(err.Error(), "no key with the fingerprint") {
		t.Errorf("a key file without the pinned key: %v", err)
	}
}

// Fedora's form, with the two things the cleartext framework changes: a
// dash-escaped line and trailing spaces.
func TestACleartextSignatureGivesBackOnlyWhatWasSigned(t *testing.T) {
	_, rsa := testKeys(t)
	message := pgpFixture(t, "checksum.txt.asc")
	text, err := verifyCleartext(rsa, message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "# a comment\n- a line starting with a dash\nSHA256 (archive.bin) = ") {
		t.Errorf("text = %q", text)
	}
	if _, err := verifyCleartext(rsa, bytes.ReplaceAll(message, []byte("\n"), []byte("\r\n"))); err != nil {
		t.Errorf("with CRLF line endings: %v", err)
	}
	// Text before the signed message is not part of it, and not returned.
	text, err = verifyCleartext(rsa, append([]byte("SHA256 (archive.bin) = "+strings.Repeat("0", 64)+"\n"), message...))
	if err != nil || strings.Contains(text, strings.Repeat("0", 64)) {
		t.Errorf("text before the message: %q, %v", text, err)
	}
	if _, err := verifyCleartext(rsa, pgpFixture(t, "archive.bin.asc")); err == nil {
		t.Error("a detached signature was taken for a signed message")
	}
}

func TestMalformedSignaturesAreRefused(t *testing.T) {
	_, rsa := testKeys(t)
	sig := pgpFixture(t, "archive.bin.sig")
	for name, bad := range map[string][]byte{
		"empty":             nil,
		"truncated":         sig[:len(sig)/2],
		"not pgp":           []byte("hello"),
		"two":               append(append([]byte{}, sig...), sig...),
		"armor without end": []byte("-----BEGIN PGP SIGNATURE-----\n\niQ==\n"),
	} {
		if err := verifyDetached(rsa, strings.NewReader("x"), bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
