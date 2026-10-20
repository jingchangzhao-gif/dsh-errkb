package service

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCredentialsRoundTripAndEscape covers the storage format the installer
// writes: a complete document, a DSN that escapes special characters, and a
// redacted description that never contains the password.
func TestCredentialsRoundTripAndEscape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	credentials := Credentials{
		Version:   1,
		Port:      55432,
		Database:  "errkb",
		Superuser: "postgres",
		Password:  "abc/@:value?",
		DataDir:   filepath.Join(dir, "data"),
	}
	if err := writeCredentials(path, credentials); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := readCredentials(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if loaded.Password != credentials.Password || loaded.Port != credentials.Port || loaded.Database != credentials.Database {
		t.Fatalf("round trip lost fields: %+v", loaded)
	}
	dsn := loaded.DSN()
	if strings.Contains(dsn, "abc/@:value?") {
		t.Fatalf("the DSN does not escape the password: %s", dsn)
	}
	for _, escaped := range []string{"%40", "%2F", "%3A", "%3F"} {
		if !strings.Contains(dsn, escaped) {
			t.Fatalf("the DSN escaping is missing %s: %s", escaped, dsn)
		}
	}
	if strings.Contains(loaded.Redacted(), credentials.Password) {
		t.Fatalf("the redacted description leaks the password")
	}
	if !strings.Contains(loaded.Redacted(), "password stored") {
		t.Fatalf("the redacted description is not explicit: %q", loaded.Redacted())
	}
}

func TestGeneratePasswordIsRandomAndLongEnough(t *testing.T) {
	first, err := generatePassword()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	second, err := generatePassword()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if first == second {
		t.Fatalf("two generated passwords are identical")
	}
	if len(first) < 16 {
		t.Fatalf("the generated password is too short: %d characters", len(first))
	}
	if first == "postgres" || strings.Contains(strings.ToLower(first), "password") {
		t.Fatalf("the generated password looks like a default: %q", first)
	}
}

func TestRemovePostgresDataDirRefusesNonClusterDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := removePostgresDataDir(dir); err == nil {
		t.Fatalf("removal accepted a directory that is not a cluster")
	}
}
