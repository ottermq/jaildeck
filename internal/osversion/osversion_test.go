package osversion

import (
	"context"
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		release string
		patch   int
		ok      bool
	}{
		{"15.1-RELEASE-p6", "15.1-RELEASE", 6, true},
		{"15.1-RELEASE", "15.1-RELEASE", 0, true},
		{"14.3-RELEASE-p12", "14.3-RELEASE", 12, true},
		{"16.0-CURRENT", "16.0-CURRENT", 0, true},
		{"15.0-STABLE", "15.0-STABLE", 0, true},
		{"", "", 0, false},
		{"15.1-RELEASE-p", "", 0, false},
		{"15.1-release-p6", "", 0, false},
		{" 15.1-RELEASE-p6", "", 0, false},
		{"15.1-RELEASE-p6\n", "", 0, false},
		{"15.1-RELEASE-p6; rm -rf /", "", 0, false},
		{"15-RELEASE-p6", "", 0, false},
		{"15.1-RELEASE-p99999999999999999999999", "", 0, false},
	}
	for _, c := range cases {
		v, ok := Parse(c.in)
		if ok != c.ok || v.Release != c.release || v.Patch != c.patch {
			t.Errorf("Parse(%q) = %+v, %v; want {%s %d}, %v", c.in, v, ok, c.release, c.patch, c.ok)
		}
	}
}

func TestCompare(t *testing.T) {
	const host = "15.1-RELEASE-p6"
	cases := []struct {
		name     string
		host     string
		userland string
		want     Standing
	}{
		{"current", host, "15.1-RELEASE-p6", Standing{Version: "15.1-RELEASE-p6", Status: Current, Host: host}},
		{"behind", host, "15.1-RELEASE-p2", Standing{Version: "15.1-RELEASE-p2", Status: Behind, Levels: 4, Host: host}},
		{"behind a release with no patch level", host, "15.1-RELEASE", Standing{Version: "15.1-RELEASE", Status: Behind, Levels: 6, Host: host}},
		{"ahead", "15.1-RELEASE-p5", "15.1-RELEASE-p6", Standing{Version: "15.1-RELEASE-p6", Status: Ahead, Levels: 1, Host: "15.1-RELEASE-p5"}},
		{"other release", host, "14.3-RELEASE-p2", Standing{Version: "14.3-RELEASE-p2", Status: OtherRelease, Host: host}},
		{"other branch", host, "15.1-STABLE", Standing{Version: "15.1-STABLE", Status: OtherRelease, Host: host}},
		{"unreadable userland", host, "", Standing{Status: Unknown, Host: host}},
		{"malformed userland", host, "not a version", Standing{Status: Unknown, Host: host}},
		{"unknown host", "", "15.1-RELEASE-p2", Standing{Version: "15.1-RELEASE-p2", Status: Unknown}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Compare(c.host, c.userland); got != c.want {
				t.Errorf("Compare(%q, %q) = %+v; want %+v", c.host, c.userland, got, c.want)
			}
		})
	}
}

func TestNewHost(t *testing.T) {
	cases := []struct {
		name                         string
		running, installed, userland string
		want                         Host
	}{
		{"booted into the installed kernel", "15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p6",
			Host{"15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p6", false}},
		{"updated, not restarted", "15.1-RELEASE-p5", "15.1-RELEASE-p6", "15.1-RELEASE-p6",
			Host{"15.1-RELEASE-p5", "15.1-RELEASE-p6", "15.1-RELEASE-p6", true}},
		{"userland behind the kernel is normal", "15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p5",
			Host{"15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p5", false}},
		{"installed kernel unreadable", "15.1-RELEASE-p6", "", "15.1-RELEASE-p6",
			Host{"15.1-RELEASE-p6", "", "15.1-RELEASE-p6", false}},
		{"garbage dropped", "15.1-RELEASE-p6", "freebsd-version: unable to locate kernel", "x",
			Host{"15.1-RELEASE-p6", "", "", false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NewHost(c.running, c.installed, c.userland); got != c.want {
				t.Errorf("NewHost = %+v; want %+v", got, c.want)
			}
		})
	}
}

func TestUserlandLine(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
		ok     bool
	}{
		{"the real shape", "#!/bin/sh\n#\n# SPDX\nset -e\n\nUSERLAND_VERSION=\"15.1-RELEASE-p2\"\n\n: ${ROOT:=}\n", "15.1-RELEASE-p2", true},
		{"no line", "#!/bin/sh\necho hello\n", "", false},
		{"commented out", "# USERLAND_VERSION=\"15.1-RELEASE-p2\"\n", "", false},
		{"not a version", "USERLAND_VERSION=\"$(id)\"\n", "", false},
		{"unquoted", "USERLAND_VERSION=15.1-RELEASE-p2\n", "", false},
		{"trailing command", "USERLAND_VERSION=\"15.1-RELEASE-p2\"; id\n", "", false},
		{"first of two wins", "USERLAND_VERSION=\"15.1-RELEASE-p2\"\nUSERLAND_VERSION=\"15.1-RELEASE-p9\"\n", "15.1-RELEASE-p2", true},
		{"CRLF", "USERLAND_VERSION=\"15.1-RELEASE-p2\"\r\n", "15.1-RELEASE-p2", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := UserlandLine([]byte(c.script))
			if got != c.want || ok != c.ok {
				t.Errorf("UserlandLine = %q, %v; want %q, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

type fakeSource struct {
	host  Host
	trees map[string]string
	reads int
}

func (f *fakeSource) HostVersions(context.Context) (Host, error) { return f.host, nil }

func (f *fakeSource) Userland(_ context.Context, root string) (string, error) {
	f.reads++
	if v, ok := f.trees[root]; ok {
		return v, nil
	}
	return "", errors.New("no such tree")
}

func TestJudge(t *testing.T) {
	source := &fakeSource{
		host:  NewHost("15.1-RELEASE-p6", "15.1-RELEASE-p6", "15.1-RELEASE-p6"),
		trees: map[string]string{"/j/a": "15.1-RELEASE-p6", "/j/b": "15.1-RELEASE-p2"},
	}
	host, got := Judge(context.Background(), source, []string{"/j/a", "/j/b", "/j/broken", "", "/j/a"})
	if host.Userland != "15.1-RELEASE-p6" {
		t.Errorf("host = %+v", host)
	}
	want := map[string]Standing{
		"/j/a":      {Version: "15.1-RELEASE-p6", Status: Current, Host: "15.1-RELEASE-p6"},
		"/j/b":      {Version: "15.1-RELEASE-p2", Status: Behind, Levels: 4, Host: "15.1-RELEASE-p6"},
		"/j/broken": {Status: Unknown, Host: "15.1-RELEASE-p6"},
	}
	if len(got) != len(want) {
		t.Fatalf("Judge = %+v; want %+v", got, want)
	}
	for root, w := range want {
		if got[root] != w {
			t.Errorf("Judge[%s] = %+v; want %+v", root, got[root], w)
		}
	}
	// One read per tree, however often it is named; none for an empty root.
	if source.reads != 3 {
		t.Errorf("reads = %d; want 3", source.reads)
	}
}
