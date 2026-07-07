package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Host string
	Port string
}

func LoadConfig() *Config {
	_ = godotenv.Load()

	return &Config{
		Host: getEnv("JAILDECK_HOST", ""),
		Port: getEnv("JAILDECK_PORT", "8888"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
