package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Postgres  PostgresConfig
	Server    ServerConfig
	Storage   StorageConfig
	Bootstrap BootstrapConfig
	Logging   LoggingConfig
}

type PostgresConfig struct {
	DSN string
}

type ServerConfig struct {
	Listen string
}

type StorageConfig struct {
	DataDir       string
	MasterKeyPath string
}

type BootstrapConfig struct {
	DisplayName   string
	OwnerUsername string
}

type LoggingConfig struct {
	Level string
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg, err := parseYAML(string(data))
	if err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseYAML(input string) (Config, error) {
	var cfg Config
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, ":") {
			section = strings.TrimSuffix(line, ":")
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return Config{}, fmt.Errorf("invalid config line %q", line)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		switch section {
		case "postgres":
			if key == "dsn" {
				cfg.Postgres.DSN = value
			}
		case "server":
			if key == "listen" {
				cfg.Server.Listen = value
			}
		case "storage":
			switch key {
			case "data_dir":
				cfg.Storage.DataDir = value
			case "master_key_path":
				cfg.Storage.MasterKeyPath = value
			}
		case "bootstrap":
			switch key {
			case "display_name":
				cfg.Bootstrap.DisplayName = value
			case "owner_username":
				cfg.Bootstrap.OwnerUsername = value
			}
		case "logging":
			if key == "level" {
				cfg.Logging.Level = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("scan config: %w", err)
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = "127.0.0.1:8080"
	}
	if c.Storage.DataDir == "" {
		c.Storage.DataDir = "/var/lib/ocservapi"
	}
	if c.Bootstrap.DisplayName == "" {
		c.Bootstrap.DisplayName = "ocservapi"
	}
	if c.Bootstrap.OwnerUsername == "" {
		c.Bootstrap.OwnerUsername = "owner"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
}

func (c Config) validate() error {
	if c.Postgres.DSN == "" {
		return fmt.Errorf("postgres.dsn is required")
	}
	if c.Storage.MasterKeyPath == "" {
		return fmt.Errorf("storage.master_key_path is required")
	}
	return nil
}
