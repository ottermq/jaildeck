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
	warningMessage = "listening on %s, which is not loopback. Without TLS in front of Jail Deck the operator password crosses the network in plaintext, and no browser can stay signed in: the session cookie is Secure, so plain HTTP discards it. Use a TLS-terminating reverse proxy, or an SSH LocalForward for ad-hoc access."
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
	LogLevel string
	// ZFSPool names the pool holding the jails layout. Empty means discover,
	// which adopts a pool only when it is the only one.
	ZFSPool string
}

func LoadConfig() *Config {
	_ = godotenv.Load()

	return &Config{
		Host:     getEnv("JAILDECK_HOST", "127.0.0.1"),
		Port:     getEnv("JAILDECK_PORT", "8888"),
		Adapter:  getEnv("JAILDECK_ADAPTER", AdapterFreeBSD),
		StateDir: getEnv("JAILDECK_STATE_DIR", "/var/db/jaildeck"),
		AuditLog: getEnv("JAILDECK_AUDIT_LOG", "/var/log/jaildeck/audit.log"),
		LogLevel: getEnv("JAILDECK_LOG_LEVEL", "info"),
		ZFSPool:  getEnv("JAILDECK_ZFS_POOL", ""),
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
