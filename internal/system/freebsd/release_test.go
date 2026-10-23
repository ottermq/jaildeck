package freebsd

import "testing"

// The mirror publishes no -pN directory: .../15.1-RELEASE-p5/base.txz is a 404
// while .../15.1-RELEASE/base.txz is not. The patch level is what
// freebsd-update brings the extracted userland to afterwards.
func TestReleaseOf(t *testing.T) {
	tests := []struct{ version, want string }{
		{"15.1-RELEASE-p5", "15.1-RELEASE"},
		{"15.1-RELEASE-p12", "15.1-RELEASE"},
		{"15.1-RELEASE", "15.1-RELEASE"},
		{"15.1-RELEASE-p5\n", "15.1-RELEASE"},
		// Neither has a patch level, and neither may lose its suffix.
		{"16.0-CURRENT", "16.0-CURRENT"},
		{"15.1-STABLE", "15.1-STABLE"},
		// -p with no digits after it is part of a name, not a patch level.
		{"15.1-RELEASE-pre", "15.1-RELEASE-pre"},
	}
	for _, tt := range tests {
		if got := releaseOf(tt.version); got != tt.want {
			t.Errorf("releaseOf(%q) = %q, want %q", tt.version, got, tt.want)
		}
	}
}

// The first three lines of the real 15.1-RELEASE MANIFEST, fetched from
// download.freebsd.org. Tab-separated, filename first and SHA256 second; the
// rest belongs to bsdinstall's component menu.
const realManifest = "base-dbg.txz\t5853d1ed94f4cbf773db8d3dcdf2a2603ded46010d5d273ec6c0c80386158905\t1777\tbase_dbg\t\"Base system debug info\"\toff\n" +
	"base.txz\t3768988b151c20f965679062b065c63a977d6bbb9f47fd83695ec2c40790c18f\t30469\tbase\t\"Base system (MANDATORY)\"\ton\n" +
	"kernel-dbg.txz\tba01c593569363a9031bc795237ac02124f114dd4c7ea1c5b18a9957220e10fc\t857\tkernel_dbg\t\"Kernel debug info\"\ton\n"

func TestManifestSHA256(t *testing.T) {
	got, err := manifestSHA256(realManifest, "base.txz")
	if err != nil {
		t.Fatalf("manifestSHA256() error = %v", err)
	}
	const want = "3768988b151c20f965679062b065c63a977d6bbb9f47fd83695ec2c40790c18f"
	if got != want {
		t.Errorf("checksum = %q, want %q", got, want)
	}

	// base-dbg.txz is listed first and its name contains base.txz's. A prefix
	// match here would verify the archive against the wrong file's checksum
	// and reject every download.
	if got == "5853d1ed94f4cbf773db8d3dcdf2a2603ded46010d5d273ec6c0c80386158905" {
		t.Error("matched base-dbg.txz instead of base.txz")
	}
}

// No checksum is a refusal, not a pass. An archive that cannot be verified is
// extracted into a template every later jail is cloned from.
func TestManifestSHA256WithoutTheFile(t *testing.T) {
	for _, manifest := range []string{realManifest, "", "garbage\n", "base.txz\t\t1\n"} {
		if _, err := manifestSHA256(manifest, "kernel.txz"); err == nil && manifest == realManifest {
			t.Error("a MANIFEST missing the file reported no error")
		}
	}
	if _, err := manifestSHA256("base.txz\t\t1\tbase\n", "base.txz"); err == nil {
		t.Error("an empty checksum field reported no error")
	}
}
