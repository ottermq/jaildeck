package freebsd

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ottermq/jaildeck/internal/jails"
)

func TestParseSingleJailConfResolvesParenNameVariable(t *testing.T) {
	jails, _ := parseJailConf(`
web {
	host.hostname = "$(name).example.test";
	path = "/usr/local/jails/$(name)";
	ip4.addr = "192.0.2.10";
}
`, nil)

	if len(jails) != 1 {
		t.Fatal("expected exactly one jail")
	}

	if jails[0].Name != "web" {
		t.Fatalf("expected jail name web, got %q", jails[0].Name)
	}
	if jails[0].Hostname != "web.example.test" {
		t.Fatalf("expected hostname to resolve $(name), got %q", jails[0].Hostname)
	}
	if jails[0].Path != "/usr/local/jails/web" {
		t.Fatalf("expected path to resolve $(name), got %q", jails[0].Path)
	}
}

func TestResolveVars(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	for _, tt := range []struct {
		name    string
		value   string
		jail    string
		want    string
		wantLog bool
	}{
		{name: "bare name", value: "$name", want: "web"},
		{name: "braced name", value: "${name}", want: "web"},
		{name: "parenthesized name", value: "$(name)", want: "web"},
		{name: "embedded and repeated names", value: "/$name/${name}/$(name)", want: "/web/web/web"},
		{name: "nameserver unchanged", value: "$nameserver", want: "$nameserver", wantLog: true},
		{name: "other unchanged", value: "$other", want: "$other", wantLog: true},
		// The jail name is substituted verbatim. Regexp.ReplaceAllString would
		// read "$1" in the replacement as a capture reference and expand it to
		// nothing; parseJailConf takes the name from whatever precedes "{" and
		// does not validate it, so a block headed "$1 {" reaches here.
		{name: "dollar in the jail name is not expanded", value: "/jails/$name", jail: "$1", want: "/jails/$1", wantLog: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()
			jail := tt.jail
			if jail == "" {
				jail = "web"
			}
			if got := resolveVars(tt.value, jail); got != tt.want {
				t.Errorf("resolveVars(%q, %q) = %q, want %q", tt.value, jail, got, tt.want)
			}
			if !tt.wantLog {
				if logs.Len() != 0 {
					t.Errorf("resolveVars(%q, %q) logged %s, want nothing", tt.value, jail, logs.String())
				}
				return
			}
			{
				var entry map[string]any
				if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
					t.Fatalf("decode unresolved variable log: %v", err)
				}
				for key, want := range map[string]string{
					"level":     "DEBUG",
					"msg":       "jail.conf value has an unresolved variable",
					"component": "freebsd",
					"jail":      jail,
					"value":     tt.want,
				} {
					if got := entry[key]; got != want {
						t.Errorf("log field %q = %v, want %q", key, got, want)
					}
				}
			}
		})
	}
}

func TestStripComment(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		want string
	}{
		{name: "trailing comment", line: `path = "/x"; # note`, want: `path = "/x"; `},
		{name: "quoted hash", line: `host.hostname = "a#b";`, want: `host.hostname = "a#b";`},
		{name: "quoted hash and trailing comment", line: `host.hostname = "a#b"; # note`, want: `host.hostname = "a#b"; `},
		{name: "unterminated quote", line: `host.hostname = "a#b`, want: `host.hostname = "a#b`},
		{name: "single quoted hash", line: `host.hostname = 'a#b';`, want: `host.hostname = 'a#b';`},
		// An apostrophe inside a double-quoted value must not close it, which is
		// why the opening quote character is tracked rather than toggled.
		{name: "apostrophe inside double quotes", line: `host.hostname = "it's #1";`, want: `host.hostname = "it's #1";`},
		// An escaped quote must not close the value either, or the # that
		// follows would be read as a comment.
		{name: "escaped quote inside value", line: `host.hostname = "a\"b#c";`, want: `host.hostname = "a\"b#c";`},
		{name: "escaped hash outside quotes", line: `ip4 = a\#b;`, want: `ip4 = a\#b;`},
		{name: "whole line comment", line: `# just a note`, want: ``},
		{name: "no comment", line: `ip4 = inherit;`, want: `ip4 = inherit;`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripComment(tt.line); got != tt.want {
				t.Errorf("stripComment(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

// The two unit fixes have to compose through the parser into the fields the API
// actually serves: a "#" inside a quoted hostname, and a bare $name in a path.
func TestParseJailConfResolvesBareNameAndKeepsQuotedHash(t *testing.T) {
	jails, _ := parseJailConf(`
web {
	host.hostname = "a#b";
	path = "/jails/$name";
	ip4 = inherit;
}
`, nil)

	if len(jails) != 1 {
		t.Fatalf("parsed %d jails, want 1", len(jails))
	}
	got := jails[0]
	if got.Name != "web" {
		t.Errorf("Name = %q, want %q", got.Name, "web")
	}
	if got.Hostname != "a#b" {
		t.Errorf("Hostname = %q, want %q — a # inside a quoted value is not a comment", got.Hostname, "a#b")
	}
	if got.Path != "/jails/web" {
		t.Errorf("Path = %q, want %q — the bare $name form must resolve", got.Path, "/jails/web")
	}
}

func TestListJailsFromConfDir(t *testing.T) {
	t.Run("loads conf files and ignores other files", func(t *testing.T) {
		dir := t.TempDir()
		for filename, content := range map[string]string{
			"a.conf":      "web {\npath = /jails/web;\n}\n",
			"b.conf":      "db {\n}\ncache {\n}\n",
			"ignored.txt": "ignored {\n}\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := listJailsFromConfDir(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []jails.Jail{
			{Name: "web", Hostname: "web", Path: "/jails/web", Status: jails.JailStatusStopped},
			{Name: "db", Hostname: "db", Status: jails.JailStatusStopped},
			{Name: "cache", Hostname: "cache", Status: jails.JailStatusStopped},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("listJailsFromConfDir() = %#v, want %#v", got, want)
		}
	})
	t.Run("propagates read errors", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "foo.conf"), 0700); err != nil {
			t.Fatal(err)
		}
		got, err := listJailsFromConfDir(dir, nil)
		if got != nil || err == nil || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("listJailsFromConfDir() = %#v, %v; want nil and a non-NotExist error", got, err)
		}
	})
}

func TestListJailsFromConfFile(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		got, _, err := listJailsFromConfFile(filepath.Join(t.TempDir(), "missing.conf"))
		if got != nil || err != nil {
			t.Fatalf("listJailsFromConfFile() = %#v, %v; want nil, nil", got, err)
		}
	})
	t.Run("propagates read errors", func(t *testing.T) {
		got, _, err := listJailsFromConfFile(t.TempDir())
		if got != nil || err == nil || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("listJailsFromConfFile() = %#v, %v; want nil and a non-NotExist error", got, err)
		}
	})
}

func TestListConfiguredJails(t *testing.T) {
	t.Run("directory wins over file by name", func(t *testing.T) {
		dir := t.TempDir()
		confFile := filepath.Join(t.TempDir(), "jail.conf")
		if err := os.WriteFile(filepath.Join(dir, "web.conf"), []byte("web {\npath = /from-dir;\n}\ndir-only {\n}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(confFile, []byte("web {\npath = /from-file;\n}\nfile-only {\n}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		a := &Adapter{confDir: dir, confFile: confFile}
		got, err := a.listConfiguredJails()
		if err != nil {
			t.Fatal(err)
		}
		want := []jails.Jail{
			{Name: "web", Hostname: "web", Path: "/from-dir", Status: jails.JailStatusStopped},
			{Name: "dir-only", Hostname: "dir-only", Status: jails.JailStatusStopped},
			{Name: "file-only", Hostname: "file-only", Status: jails.JailStatusStopped},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("listConfiguredJails() = %#v, want %#v", got, want)
		}
	})
	for _, source := range []string{"directory", "file"} {
		t.Run(source+" read error", func(t *testing.T) {
			a := &Adapter{confDir: t.TempDir(), confFile: filepath.Join(t.TempDir(), "missing.conf")}
			if source == "directory" {
				if err := os.Mkdir(filepath.Join(a.confDir, "foo.conf"), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				a.confFile = t.TempDir()
			}
			got, err := a.listConfiguredJails()
			if got != nil || err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("listConfiguredJails() = %#v, %v; want nil and a non-NotExist error", got, err)
			}
		})
	}
}

func TestNewAdapterUsesSystemConfPaths(t *testing.T) {
	a := NewAdapter(nil)
	if a.confFile != defaultJailConf || a.confDir != defaultJailConfDir {
		t.Errorf("NewAdapter() conf paths = %q, %q; want %q, %q",
			a.confFile, a.confDir, defaultJailConf, defaultJailConfDir)
	}
}

// The configuration this reproduces is the one on the project's FreeBSD test
// VM, and the layout the handbook recommends: shared defaults at the top of
// jail.conf, one small file per jail in jail.conf.d. Before #51 every jail
// configured this way reported an empty path, and ip4's mode as its address.
func TestListConfiguredJailsInheritsSharedDefaults(t *testing.T) {
	dir := t.TempDir()
	confFile := filepath.Join(t.TempDir(), "jail.conf")

	if err := os.WriteFile(confFile, []byte(`# Shared defaults applied to every jail
exec.start = "/bin/sh /etc/rc";
exec.stop = "/bin/sh /etc/rc.shutdown";
exec.clean;
mount.devfs;
host.hostname = "${name}";
path = "/usr/local/jails/containers/${name}";
interface = vtnet0;

.include "/etc/jail.conf.d/*.conf";
`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "ottermq.conf"), []byte(`ottermq {
  ip4 = inherit;
}
`), 0600); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{confDir: dir, confFile: confFile}
	got, err := a.listConfiguredJails()
	if err != nil {
		t.Fatalf("listConfiguredJails: %v", err)
	}

	want := []jails.Jail{{
		Name:     "ottermq",
		Hostname: "ottermq",
		// From the shared default, with ${name} expanded for this jail.
		Path: "/usr/local/jails/containers/ottermq",
		// ip4 = inherit names a mode, not an address: the jail has none of its
		// own, which is also what jls reports for it.
		IP:     "",
		Status: jails.JailStatusStopped,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listConfiguredJails() = %#v, want %#v", got, want)
	}
}

func TestParseJailConfInheritsTopLevelSettings(t *testing.T) {
	t.Run("a block inherits what it does not set", func(t *testing.T) {
		got, _ := parseJailConf(`
path = "/pool/${name}";
host.hostname = "${name}.example.test";
web {
}
`, nil)
		want := []jails.Jail{{
			Name: "web", Hostname: "web.example.test",
			Path: "/pool/web", Status: jails.JailStatusStopped,
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseJailConf() = %#v, want %#v", got, want)
		}
	})

	t.Run("a block overrides an inherited setting", func(t *testing.T) {
		got, _ := parseJailConf(`
path = "/pool/${name}";
web {
	path = "/elsewhere/web";
}
`, nil)
		if got[0].Path != "/elsewhere/web" {
			t.Errorf("Path = %q, want the block's own value", got[0].Path)
		}
	})

	t.Run("every block inherits, not only the first", func(t *testing.T) {
		got, _ := parseJailConf(`
path = "/pool/${name}";
web {
}
db {
}
`, nil)
		if len(got) != 2 {
			t.Fatalf("parsed %d jails, want 2", len(got))
		}
		// ${name} is expanded per jail, so one default cannot be resolved once
		// and shared.
		if got[0].Path != "/pool/web" || got[1].Path != "/pool/db" {
			t.Errorf("paths = %q, %q; want /pool/web, /pool/db", got[0].Path, got[1].Path)
		}
	})

	t.Run("defaults from an enclosing file are inherited", func(t *testing.T) {
		got, _ := parseJailConf("web {\n}\n", map[string]string{"path": "/inherited/${name}"})
		if got[0].Path != "/inherited/web" {
			t.Errorf("Path = %q, want the inherited default", got[0].Path)
		}
	})

	t.Run("top-level settings are returned for the files this one includes", func(t *testing.T) {
		_, defaults := parseJailConf("path = \"/pool/${name}\";\n", nil)
		if defaults["path"] != "/pool/${name}" {
			t.Errorf("defaults[path] = %q, want the raw unresolved value", defaults["path"])
		}
	})
}

func TestParseJailConfReadsAddressNotMode(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		want    string
	}{
		{"ip4.addr is the address", "web {\nip4.addr = \"192.0.2.10\";\n}\n", "192.0.2.10"},
		{"ip4 is a mode, not an address", "web {\nip4 = inherit;\n}\n", ""},
		{"a mode does not mask an address", "web {\nip4 = new;\nip4.addr = \"192.0.2.11\";\n}\n", "192.0.2.11"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := parseJailConf(tt.content, nil)
			if got[0].IP != tt.want {
				t.Errorf("IP = %q, want %q", got[0].IP, tt.want)
			}
		})
	}
}

// Each file in jail.conf.d is parsed with the same inherited defaults, so one
// file's own top-level settings must not reach its siblings.
func TestListJailsFromConfDirIsolatesPerFileSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.conf"), []byte("path = \"/from-a/${name}\";\nweb {\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.conf"), []byte("db {\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Non-nil defaults are what makes the leak observable: a.conf overrides
	// path at its own top level, and b.conf must still see the inherited value
	// rather than a.conf's.
	got, err := listJailsFromConfDir(dir, map[string]string{"path": "/inherited/${name}"})
	if err != nil {
		t.Fatal(err)
	}
	want := []jails.Jail{
		{Name: "web", Hostname: "web", Path: "/from-a/web", Status: jails.JailStatusStopped},
		{Name: "db", Hostname: "db", Path: "/inherited/db", Status: jails.JailStatusStopped},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listJailsFromConfDir() = %#v, want %#v", got, want)
	}
}
