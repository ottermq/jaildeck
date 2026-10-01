package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Adapter names the jail-system implementation the application wires up.
const (
	AdapterFreeBSD = "freebsd"
	AdapterFake    = "fake"
	warningMessage = `WARNING: listening on %s, which is not loopback. If nothing is terminating
TLS in front of Jail Deck, the operator password crosses the network in
plaintext. Use a reverse proxy terminating TLS for a permanent deployment, or an
SSH LocalForward for ad-hoc access.`
)

type Config struct {
	Host string
	Port string
	// Adapter selects the jails.JailSystem implementation: AdapterFreeBSD
	// (default, requires a real FreeBSD host) or AdapterFake (in-memory,
	// for development and verification on any OS).
	Adapter  string
	StateDir string
	AuditLog string
}

func LoadConfig() *Config {
	_ = godotenv.Load()

	return &Config{
		Host:     getEnv("JAILDECK_HOST", "127.0.0.1"),
		Port:     getEnv("JAILDECK_PORT", "8888"),
		Adapter:  getEnv("JAILDECK_ADAPTER", AdapterFreeBSD),
		StateDir: getEnv("JAILDECK_STATE_DIR", "/var/db/jaildeck"),
		AuditLog: getEnv("JAILDECK_AUDIT_LOG", "/var/log/jaildeck/audit.log"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func BindWarning(host string) string {
	if !isLoopback(host) {
		if host == "" {
			host = "all interfaces"
		}
		return fmt.Sprintf(warningMessage, host)
	}
	return ""
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
