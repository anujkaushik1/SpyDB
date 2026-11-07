package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
)

// dbConfigFile holds the Postgres connection settings. It is gitignored because
// it contains the password; copy db.config.example.json to create it. The path
// is relative to the working directory, which is the project root under
// `wails3 dev`.
const dbConfigFile = "db.config.json"

type DBConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
}

// loadDBConfig reads dbConfigFile on every call, so edits apply without a restart.
func loadDBConfig() (DBConfig, error) {
	var cfg DBConfig
	data, err := os.ReadFile(dbConfigFile)
	if err != nil {
		return cfg, fmt.Errorf("%w (copy db.config.example.json to %s)", err, dbConfigFile)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", dbConfigFile, err)
	}
	return cfg, nil
}

// connString builds a postgres:// URL, escaping the user, password and database.
func (c DBConfig) connString() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:   c.Database,
	}
	return u.String()
}
