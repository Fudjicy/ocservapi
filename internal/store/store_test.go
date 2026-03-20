package store

import "testing"

func TestSafeDSN(t *testing.T) {
	got := SafeDSN("postgres://alice:secret@localhost:5432/ocservapi?sslmode=disable&password=secret")
	want := "postgres://alice@localhost:5432/ocservapi?password=redacted&sslmode=disable"
	if got != want {
		t.Fatalf("SafeDSN() = %q, want %q", got, want)
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	salt, hash := hashPassword("secret123")
	if !verifyPassword("secret123", salt, hash) {
		t.Fatal("expected password verification to succeed")
	}
	if verifyPassword("wrong", salt, hash) {
		t.Fatal("expected wrong password verification to fail")
	}
}
