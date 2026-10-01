package config

import "testing"

func TestFromEnv(t *testing.T) {
	t.Setenv("NEO4J_URI", "bolt://localhost:7687")
	t.Setenv("NEO4J_USERNAME", "neo4j")
	t.Setenv("NEO4J_PASSWORD", "test-password")
	t.Setenv("NEO4J_DATABASE", "cmdb-test")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URI != "bolt://localhost:7687" || cfg.Username != "neo4j" || cfg.Password != "test-password" || cfg.Database != "cmdb-test" {
		t.Fatalf("FromEnv() = %#v", cfg)
	}
}

func TestFromEnvRequiresConnectionSettings(t *testing.T) {
	t.Setenv("NEO4J_URI", "")
	t.Setenv("NEO4J_USERNAME", "")
	t.Setenv("NEO4J_PASSWORD", "")
	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv() succeeded without connection settings")
	}
}
