package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name string
		env  Config
		want Config
	}{
		{
			name: "defaults for empty environment variables",
			want: Config{
				Host:     "127.0.0.1",
				Port:     "8888",
				Adapter:  AdapterFreeBSD,
				StateDir: "/var/db/jaildeck",
				AuditLog: "/var/log/jaildeck/audit.log",
				LogLevel: "info",

				VMSwitch:     "jaildeck",
				VMAddress:    "10.88.0.1/24",
				PFAnchorDir:  "/usr/local/etc/jaildeck/pf",
				NetworkRange: "10.92.0.0/16",

				ConsoleIdle: 30 * time.Minute,
			},
		},
		{
			name: "environment overrides",
			env: Config{
				Host:     "0.0.0.0",
				Port:     "9090",
				Adapter:  AdapterFake,
				StateDir: "/tmp/jaildeck-state",
				AuditLog: "/tmp/jaildeck-audit.log",
				LogLevel: "debug",
				ZFSPool:  "tank",

				VMSwitch:     "tenant0",
				VMAddress:    "10.90.0.1/24",
				VMEgress:     "igb0",
				PFAnchorDir:  "/tmp/jaildeck-pf",
				NetworkRange: "10.93.0.0/16",

				ConsoleIdle:    5 * time.Minute,
				ConsoleOrigins: []string{"jaildeck.example.org", "*.example.org"},
			},
			want: Config{
				Host:     "0.0.0.0",
				Port:     "9090",
				Adapter:  AdapterFake,
				StateDir: "/tmp/jaildeck-state",
				AuditLog: "/tmp/jaildeck-audit.log",
				LogLevel: "debug",
				ZFSPool:  "tank",

				VMSwitch:     "tenant0",
				VMAddress:    "10.90.0.1/24",
				VMEgress:     "igb0",
				PFAnchorDir:  "/tmp/jaildeck-pf",
				NetworkRange: "10.93.0.0/16",

				ConsoleIdle:    5 * time.Minute,
				ConsoleOrigins: []string{"jaildeck.example.org", "*.example.org"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JAILDECK_HOST", tt.env.Host)
			t.Setenv("JAILDECK_PORT", tt.env.Port)
			t.Setenv("JAILDECK_ADAPTER", tt.env.Adapter)
			t.Setenv("JAILDECK_STATE_DIR", tt.env.StateDir)
			t.Setenv("JAILDECK_AUDIT_LOG", tt.env.AuditLog)
			t.Setenv("JAILDECK_LOG_LEVEL", tt.env.LogLevel)
			t.Setenv("JAILDECK_ZFS_POOL", tt.env.ZFSPool)
			t.Setenv("JAILDECK_VM_SWITCH", tt.env.VMSwitch)
			t.Setenv("JAILDECK_VM_ADDRESS", tt.env.VMAddress)
			t.Setenv("JAILDECK_VM_EGRESS", tt.env.VMEgress)
			t.Setenv("JAILDECK_PF_ANCHOR_DIR", tt.env.PFAnchorDir)
			t.Setenv("JAILDECK_NETWORK_RANGE", tt.env.NetworkRange)
			idle := ""
			if tt.env.ConsoleIdle != 0 {
				idle = tt.env.ConsoleIdle.String()
			}
			t.Setenv("JAILDECK_CONSOLE_IDLE", idle)
			t.Setenv("JAILDECK_CONSOLE_ORIGINS", strings.Join(tt.env.ConsoleOrigins, " , "))

			if got := LoadConfig(); got == nil || !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("LoadConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBindWarning(t *testing.T) {
	tests := []struct {
		host         string
		wantWarning  bool
		wantMentions string
	}{
		{host: "localhost", wantWarning: false},
		{host: "LOCALHOST", wantWarning: false},
		{host: "127.0.0.1", wantWarning: false},
		{host: "::1", wantWarning: false},
		{host: "0.0.0.0", wantWarning: true},
		{host: "::", wantWarning: true},
		{host: "192.168.1.10", wantWarning: true},
		{host: "example.com", wantWarning: true},
		{host: "", wantWarning: true, wantMentions: "all interfaces"},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := BindWarning(tt.host)
			if tt.wantWarning {
				mentions := tt.wantMentions
				if mentions == "" {
					mentions = tt.host
				}
				if !strings.HasPrefix(got, "listening on "+mentions) {
					t.Errorf("BindWarning(%q) = %q, want it to open by naming %q", tt.host, got, mentions)
				}
				// The warning's lede: on this bind a browser cannot hold a
				// session at all, which is what the plaintext note alone left
				// people to discover for themselves (#27).
				if !strings.Contains(got, "stay signed in") {
					t.Errorf("BindWarning(%q) = %q, want it to say a browser cannot stay signed in", tt.host, got)
				}
			} else if got != "" {
				t.Errorf("BindWarning(%q) = %q, want no warning", tt.host, got)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "LoCaLhOsT", want: true},
		{host: "127.0.0.1", want: true},
		{host: "127.1.2.3", want: true},
		{host: "::1", want: true},
		{host: "0:0:0:0:0:0:0:1", want: true},
		{host: "::ffff:127.0.0.1", want: true},
		{host: "0.0.0.0", want: false},
		{host: "::", want: false},
		{host: "192.168.1.10", want: false},
		{host: "2001:db8::1", want: false},
		{host: "::ffff:192.168.1.10", want: false},
		{host: "example.com", want: false},
		{host: "127.0.0.999", want: false},
		{host: "127.0.0.1:8888", want: false},
		{host: "[::1]", want: false},
		{host: " localhost ", want: false},
		{host: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := isLoopback(tt.host); got != tt.want {
				t.Errorf("isLoopback(%q) = %t, want %t", tt.host, got, tt.want)
			}
		})
	}
}

// A duration that does not parse keeps the default rather than turning the
// timeout off; "0" turns it off on purpose.
func TestConsoleIdle(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"":     30 * time.Minute,
		"0":    0,
		"2h":   2 * time.Hour,
		"soon": 30 * time.Minute,
		"-5m":  30 * time.Minute,
		"90s":  90 * time.Second,
	} {
		t.Setenv("JAILDECK_CONSOLE_IDLE", value)
		if got := LoadConfig().ConsoleIdle; got != want {
			t.Errorf("JAILDECK_CONSOLE_IDLE=%q: %s, want %s", value, got, want)
		}
	}
}

// JAILDECK_CONSOLE_UI is on unless it says off, and anything it says that is
// neither is off: a typo must not leave the browser console open (#155).
func TestConsoleUISwitch(t *testing.T) {
	for _, tt := range []struct {
		value string
		off   bool
	}{
		{"", false}, {"on", false}, {"ON", false}, {" on ", false},
		{"off", true}, {"Off", true}, {"of", true}, {"no", true}, {"false", true},
	} {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("JAILDECK_CONSOLE_UI", tt.value)
			if got := LoadConfig().NoConsoleUI; got != tt.off {
				t.Errorf("JAILDECK_CONSOLE_UI=%q: NoConsoleUI = %t, want %t", tt.value, got, tt.off)
			}
		})
	}
}
