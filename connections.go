package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxRecent is how many recent connections are remembered.
const maxRecent = 10

// SavedConnection is a connection the user saved by name, or (with an empty
// Name) one from the Recent list. Config includes the password: the user
// chose to keep passwords in this file, which only their account can read.
type SavedConnection struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Config   DBConfig `json:"config"`
	LastUsed int64    `json:"lastUsed"` // Unix milliseconds; 0 if never connected
}

// ConnectionLists is what the connect screen shows.
type ConnectionLists struct {
	Saved  []SavedConnection `json:"saved"`
	Recent []SavedConnection `json:"recent"` // newest first
}

// connectionsFile is ~/Library/Application Support/SpyDB/connections.json on
// macOS (the OS's per-user config folder elsewhere).
func connectionsFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "SpyDB", "connections.json"), nil
}

// SavedConnections returns the saved and recent connections.
func (s *DBService) SavedConnections() (ConnectionLists, error) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	return loadConnections()
}

// SaveConnection adds c to Saved, or updates the saved connection with c.ID.
func (s *DBService) SaveConnection(c SavedConnection) (SavedConnection, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return SavedConnection{}, errors.New("Give the connection a name.")
	}
	if err := c.Config.validate(); err != nil {
		return SavedConnection{}, err
	}

	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	lists, err := loadConnections()
	if err != nil {
		return SavedConnection{}, err
	}
	if c.ID == "" {
		c.ID = newID()
		lists.Saved = append(lists.Saved, c)
	} else {
		found := false
		for i := range lists.Saved {
			if lists.Saved[i].ID == c.ID {
				c.LastUsed = lists.Saved[i].LastUsed
				lists.Saved[i] = c
				found = true
			}
		}
		if !found {
			return SavedConnection{}, errors.New("That saved connection no longer exists.")
		}
	}
	return c, writeConnections(lists)
}

// DeleteConnection removes a saved connection.
func (s *DBService) DeleteConnection(id string) error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	lists, err := loadConnections()
	if err != nil {
		return err
	}
	kept := lists.Saved[:0]
	for _, c := range lists.Saved {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	lists.Saved = kept
	return writeConnections(lists)
}

// ClearRecent empties the Recent list.
func (s *DBService) ClearRecent() error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	lists, err := loadConnections()
	if err != nil {
		return err
	}
	lists.Recent = []SavedConnection{}
	return writeConnections(lists)
}

// recordRecent puts cfg at the top of Recent (one entry per server, user and
// database) and stamps matching saved connections as just used. Errors are
// ignored: Recent is a convenience and must never stop a connect.
func (s *DBService) recordRecent(cfg DBConfig) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	lists, err := loadConnections()
	if err != nil {
		return
	}
	now := time.Now().UnixMilli()
	for i := range lists.Saved {
		if sameTarget(lists.Saved[i].Config, cfg) {
			lists.Saved[i].LastUsed = now
		}
	}
	recent := []SavedConnection{{ID: newID(), Config: cfg, LastUsed: now}}
	for _, c := range lists.Recent {
		if !sameTarget(c.Config, cfg) && len(recent) < maxRecent {
			recent = append(recent, c)
		}
	}
	lists.Recent = recent
	_ = writeConnections(lists)
}

func sameTarget(a, b DBConfig) bool {
	return a.Host == b.Host && a.Port == b.Port && a.User == b.User && a.Database == b.Database
}

func loadConnections() (ConnectionLists, error) {
	lists := ConnectionLists{Saved: []SavedConnection{}, Recent: []SavedConnection{}}
	path, err := connectionsFile()
	if err != nil {
		return lists, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return lists, nil
	}
	if err != nil {
		return lists, err
	}
	if err := json.Unmarshal(data, &lists); err != nil {
		return lists, fmt.Errorf("%s is damaged: %w", path, err)
	}
	if lists.Saved == nil {
		lists.Saved = []SavedConnection{}
	}
	if lists.Recent == nil {
		lists.Recent = []SavedConnection{}
	}
	return lists, nil
}

// writeConnections replaces the file in one step (write a temporary file, then
// rename), so a crash mid-write can't leave it half written. The folder and
// file are readable only by the current user, since they hold passwords.
func writeConnections(lists ConnectionLists) error {
	path, err := connectionsFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lists, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "connections-*.json")
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

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
