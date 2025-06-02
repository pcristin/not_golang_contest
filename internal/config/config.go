package config

import (
	"flag"
	"os"
)

// NewConfig creates a new ConfigGetter
func NewConfig() *Config {
	return &Config{
		Port:        "",
		RedisURL:    "",
		PostgresURL: "",
		LogLevel:    "info",
	}
}

// ParseFlags parses the flags and sets the config
func (c *Config) ParseFlags() {
	// Build-in flags
	flag.StringVar(&c.Port, "port", "8080", "Port to listen on")
	flag.StringVar(&c.RedisURL, "redis-url", "localhost:6379", "Redis URL")
	flag.StringVar(&c.PostgresURL, "postgres-url", "postgres://localhost:5432/flash_sale?sslmode=disable", "Postgres URL")
	flag.StringVar(&c.LogLevel, "log-level", "info", "Log level")

	// Parse flags
	flag.Parse()

	// Environment variables (overrides build-in flags)
	c.LoadEnvVars()

}

// LoadEnvVars loads the environment variables and sets the config
func (c *Config) LoadEnvVars() {
	// Port
	if valuePort, foundPort := os.LookupEnv("PORT"); foundPort && valuePort != "" {
		c.Port = valuePort
	}

	// Log level
	if valueLogLevel, foundLogLevel := os.LookupEnv("LOG_LEVEL"); foundLogLevel && valueLogLevel != "" {
		c.LogLevel = valueLogLevel
	}

	// Redis URL
	if valueRedisURL, foundRedisURL := os.LookupEnv("REDIS_URL"); foundRedisURL && valueRedisURL != "" {
		c.RedisURL = valueRedisURL
	}

	// Postgres URL
	if valuePostgresURL, foundPostgresURL := os.LookupEnv("POSTGRES_URL"); foundPostgresURL && valuePostgresURL != "" {
		c.PostgresURL = valuePostgresURL
	}
}

// GetPort returns the current configuration
func (c *Config) GetPort() string {
	return c.Port
}

// GetRedisURL returns the current configuration
func (c *Config) GetRedisURL() string {
	return c.RedisURL
}

// GetPostgresURL returns the current configuration
func (c *Config) GetPostgresURL() string {
	return c.PostgresURL
}

// GetLogLevel returns the current configuration
func (c *Config) GetLogLevel() string {
	return c.LogLevel
}
