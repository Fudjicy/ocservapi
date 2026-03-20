package store

import "testing"

func TestSafeDSN(t *testing.T) {
	got := SafeDSN("postgres://alice:secret@localhost:5432/ocservapi?sslmode=disable&password=secret")
	want := "postgres://alice@localhost:5432/ocservapi?password=redacted&sslmode=disable"
	if got != want {
		t.Fatalf("SafeDSN() = %q, want %q", got, want)
	}
}
