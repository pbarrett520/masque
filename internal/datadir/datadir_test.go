package datadir

import (
	"path/filepath"
	"testing"
)

func TestPlatformDirLinuxXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	dir, err := platformDir("linux")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/custom/data", "masque"); dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
}

func TestPlatformDirLinuxDefault(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/test")
	dir, err := platformDir("linux")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/home/test", ".local", "share", "masque"); dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
}

func TestPlatformDirUnsupported(t *testing.T) {
	if _, err := platformDir("plan9"); err == nil {
		t.Error("expected error for unsupported platform")
	}
}
