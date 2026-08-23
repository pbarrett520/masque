// Package datadir resolves the platform-specific directory where Masque
// stores its data (the SQLite database, and later avatars/exports).
package datadir

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Resolve returns the Masque data directory for the current platform,
// creating it if it does not exist.
//
//	Linux:   $XDG_DATA_HOME/masque, defaulting to ~/.local/share/masque
//	macOS:   ~/Library/Application Support/Masque
//	Windows: %APPDATA%/Masque
func Resolve() (string, error) {
	dir, err := platformDir(runtime.GOOS)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating data dir %s: %w", dir, err)
	}
	return dir, nil
}

func platformDir(goos string) (string, error) {
	switch goos {
	case "linux":
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "masque"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home dir: %w", err)
		}
		return filepath.Join(home, ".local", "share", "masque"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "Masque"), nil
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(appData, "Masque"), nil
	default:
		return "", fmt.Errorf("unsupported platform %q", goos)
	}
}
