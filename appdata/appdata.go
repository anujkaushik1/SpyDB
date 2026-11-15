// Package appdata finds and writes SpyDB's files in its settings folder,
// for every database SpyDB supports.
package appdata

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Path is a file in SpyDB's settings folder: ~/Library/Application
// Support/SpyDB on macOS, ~/.config/SpyDB on Linux, %AppData%\SpyDB on
// Windows.
func Path(name string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "SpyDB", name), nil
}

// WritePrivateJSON replaces path with v as indented JSON in one step (write a
// temporary file, then rename), so a crash mid-write can't leave it half
// written. The folder and file are readable only by the current user (on
// Windows, file modes don't apply: the folder is in the user's profile,
// which Windows already keeps to that user).
func WritePrivateJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadJSON reads path into v; a file that isn't there yet leaves v as it is.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}
