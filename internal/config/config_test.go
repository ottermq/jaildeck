package config

import "testing"

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
			},
			want: Config{
				Host:     "0.0.0.0",
				Port:     "9090",
				Adapter:  AdapterFake,
				StateDir: "/tmp/jaildeck-state",
				AuditLog: "/tmp/jaildeck-audit.log",
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

			if got := LoadConfig(); got == nil || *got != tt.want {
				t.Errorf("LoadConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
