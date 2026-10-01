package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	URI      string
	Username string
	Password string
	Database string
}

func FromEnv() (Config, error) {
	cfg := Config{
		URI:      strings.TrimSpace(os.Getenv("NEO4J_URI")),
		Username: os.Getenv("NEO4J_USERNAME"),
		Password: os.Getenv("NEO4J_PASSWORD"),
		Database: strings.TrimSpace(os.Getenv("NEO4J_DATABASE")),
	}
	if cfg.URI == "" {
		return Config{}, fmt.Errorf("NEO4J_URI is required")
	}
	if cfg.Username == "" {
		return Config{}, fmt.Errorf("NEO4J_USERNAME is required")
	}
	if cfg.Password == "" {
		return Config{}, fmt.Errorf("NEO4J_PASSWORD is required")
	}
	return cfg, nil
}
