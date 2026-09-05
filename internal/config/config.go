package config

import (
	"os"

	"github.com/joho/godotenv"
)

// Adapter names the jail-system implementation the application wires up.
const (
	AdapterFreeBSD = "freebsd"
	AdapterFake    = "fake"
)

type Config struct {
	Host string
	Port string
	// Adapter selects the jails.JailSystem implementation: AdapterFreeBSD
	// (default, requires a real FreeBSD host) or AdapterFake (in-memory,
	// for development and verification on any OS).
	Adapter string
}

func LoadConfig() *Config {
	_ = godotenv.Load()

	return &Config{
		Host:    getEnv("JAILDECK_HOST", "127.0.0.1"),
		Port:    getEnv("JAILDECK_PORT", "8888"),
		Adapter: getEnv("JAILDECK_ADAPTER", AdapterFreeBSD),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
