package pgwire

import "testing"

func TestParseDSN(t *testing.T) {
	cfg, err := ParseDSN("postgres://alice:secret@127.0.0.1:5432/ocservapi?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "alice" || cfg.Password != "secret" || cfg.Database != "ocservapi" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
