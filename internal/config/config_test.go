package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("postgres:\n  dsn: postgres://user:pass@localhost/db\nstorage:\n  master_key_path: /tmp/master.key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:8080" {
		t.Fatalf("unexpected listen default: %q", cfg.Server.Listen)
	}
	if cfg.Bootstrap.OwnerUsername != "owner" {
		t.Fatalf("unexpected owner default: %q", cfg.Bootstrap.OwnerUsername)
	}
}
