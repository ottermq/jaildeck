package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

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
	// Socket is a unix socket to listen on beside TCP, mode 0660 (JD-002).
	// Empty means TCP only. SocketOnly drops the TCP listener, which needs a
	// flag of its own rather than an empty Host: an empty Host already means
	// "all interfaces" and overloading it would turn a typo into the widest
	// possible bind.
	//
	// SocketGroup is empty by default, which leaves the socket in the process's
	// own group -- wheel for a root service on FreeBSD, which is the group that
	// would have been named anyway. Defaulting to "wheel" literally would make
	// the socket unusable on a Linux development box, where that group does not
	// exist, for no gain on the hosts that have it.
	Socket      string
	SocketGroup string
	SocketOnly  bool

	// The default VM network (#118, JD-040): a vm-bhyve switch Jail Deck
	// owns, with VMAddress -- the host's address on it, which is the guests'
	// gateway -- and outbound NAT through the PF anchor files in PFAnchorDir.
	// VMEgress is the interface NAT goes out of; empty means the one the
	// default route uses.
	VMSwitch    string
	VMAddress   string
	VMEgress    string
	PFAnchorDir string
	// NetworkRange is the block tenant networks' subnets come from, one /24
	// each (JD-043).
	NetworkRange string

	// The browser console (JD-042). ConsoleIdle ends a session nothing has
	// been typed into for that long; 0 never does. ConsoleOrigins are hosts a
	// page may open one from besides this host itself, for a proxy that
	// rewrites Host: "jaildeck.example.org", "*.example.org".
	ConsoleIdle    time.Duration
	ConsoleOrigins []string
	// NoConsoleUI keeps the browser session from opening a console:
	// JAILDECK_CONSOLE_UI=off (#155), on a host a platform manages, whose own
	// token with the console scope is then the only way in through Jail
	// Deck. Negative so that the zero value is the default, on.
	NoConsoleUI bool
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

		Socket:      getEnv("JAILDECK_SOCKET", ""),
		SocketGroup: getEnv("JAILDECK_SOCKET_GROUP", ""),
		SocketOnly:  getEnv("JAILDECK_SOCKET_ONLY", "") == "true",

		VMSwitch:    getEnv("JAILDECK_VM_SWITCH", "jaildeck"),
		VMAddress:   getEnv("JAILDECK_VM_ADDRESS", "10.88.0.1/24"),
		VMEgress:    getEnv("JAILDECK_VM_EGRESS", ""),
		PFAnchorDir: getEnv("JAILDECK_PF_ANCHOR_DIR", "/usr/local/etc/jaildeck/pf"),

		NetworkRange: getEnv("JAILDECK_NETWORK_RANGE", "10.92.0.0/16"),

		ConsoleIdle:    getDuration("JAILDECK_CONSOLE_IDLE", 30*time.Minute),
		ConsoleOrigins: getList("JAILDECK_CONSOLE_ORIGINS"),
		NoConsoleUI:    !getSwitch("JAILDECK_CONSOLE_UI", true),
	}
}

// getDuration reads a Go duration ("30m", "2h", "0"). One that does not parse
// keeps the default and says so, rather than turning the timeout off.
func getDuration(key string, defaultValue time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		slog.Warn("ignoring a setting that is not a duration", "key", key, "value", value, "using", defaultValue.String())
		return defaultValue
	}
	return d
}

// getSwitch reads "on" or "off". Anything else is off and says so: these
// switches turn something away, and a typo should not leave it open.
func getSwitch(key string, defaultValue bool) bool {
	switch value := strings.ToLower(strings.TrimSpace(os.Getenv(key))); value {
	case "":
		return defaultValue
	case "on":
		return true
	case "off":
		return false
	default:
		slog.Warn("a setting that is neither on nor off is taken as off", "key", key, "value", value)
		return false
	}
}

// getList reads a comma-separated list, nil when unset.
func getList(key string) []string {
	var list []string
	for _, item := range strings.Split(os.Getenv(key), ",") {
		if item = strings.TrimSpace(item); item != "" {
			list = append(list, item)
		}
	}
	return list
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
