package freebsd

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestParseSingleJailConfResolvesParenNameVariable(t *testing.T) {
	jails := parseJailConf(`
web {
	host.hostname = "$(name).example.test";
	path = "/usr/local/jails/$(name)";
	ip4 = "192.0.2.10";
}
`)

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
	jails := parseJailConf(`
web {
	host.hostname = "a#b";
	path = "/jails/$name";
	ip4 = inherit;
}
`)

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
