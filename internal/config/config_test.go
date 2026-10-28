package config

import (
	"strings"
	"testing"
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

				VMSwitch:    "jaildeck",
				VMAddress:   "10.88.0.1/24",
				PFAnchorDir: "/usr/local/etc/jaildeck/pf",
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

				VMSwitch:    "tenant0",
				VMAddress:   "10.90.0.1/24",
				VMEgress:    "igb0",
				PFAnchorDir: "/tmp/jaildeck-pf",
			},
			want: Config{
				Host:     "0.0.0.0",
				Port:     "9090",
				Adapter:  AdapterFake,
				StateDir: "/tmp/jaildeck-state",
				AuditLog: "/tmp/jaildeck-audit.log",
				LogLevel: "debug",
				ZFSPool:  "tank",

				VMSwitch:    "tenant0",
				VMAddress:   "10.90.0.1/24",
				VMEgress:    "igb0",
				PFAnchorDir: "/tmp/jaildeck-pf",
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

			if got := LoadConfig(); got == nil || *got != tt.want {
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
