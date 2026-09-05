// Package config loads the CLI configuration from the user config directory.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds user-level CLI settings persisted as JSON.
type Config struct {
	OutputDir   string `json:"output_dir,omitempty"`
	WorkerCount int    `json:"worker_count,omitempty"`
}

// DefaultWorkerCount is used when neither the config file nor flags set one.
const DefaultWorkerCount = 16

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "jabledownloader")
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

// Path returns the on-disk config file location.
func Path() string {
	return configPath()
}

// Load reads the config file, returning defaults when it does not exist.
func Load() (*Config, error) {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Defaults(), nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return applyDefaults(&cfg), nil
}

// Save writes the config file, creating the directory when needed.
func (c *Config) Save() error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath(), data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Defaults returns the effective default configuration.
func Defaults() *Config {
	return applyDefaults(&Config{})
}

func applyDefaults(c *Config) *Config {
	if c.WorkerCount <= 0 {
		c.WorkerCount = DefaultWorkerCount
	}
	if c.OutputDir == "" {
		c.OutputDir = "./videos"
	}
	return c
}
