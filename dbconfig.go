package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// dbConfigFile optionally pre-fills the connection form. It is gitignored
// because it can hold a password; see db.config.example.json for the format.
// The path is relative to the working directory, which is the project root
// under `wails3 dev`.
const dbConfigFile = "db.config.json"

// appName is sent to Postgres as application_name.
const appName = "SpyDB"

// DBConfig is the connection form. Only exported fields reach the frontend,
// under the names in their json tags.
type DBConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
}

var defaultDBConfig = DBConfig{Host: "localhost", Port: 5432}

// loadDBConfig returns dbConfigFile's values, or defaultDBConfig if it doesn't exist.
func loadDBConfig() (DBConfig, error) {
	cfg := defaultDBConfig
	data, err := os.ReadFile(dbConfigFile)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultDBConfig, fmt.Errorf("parse %s: %w", dbConfigFile, err)
	}
	return cfg, nil
}

func (c DBConfig) validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("host is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}
	return nil
}

// connString builds a keyword/value DSN. Empty fields are left out so libpq
// defaults apply: no user means the OS user, no database means the user's name.
func (c DBConfig) connString() string {
	var parts []string
	add := func(key, value string) {
		if value != "" {
			parts = append(parts, key+"="+quoteDSNValue(value))
		}
	}
	add("host", strings.TrimSpace(c.Host))
	add("port", strconv.Itoa(c.Port))
	add("user", c.User)
	add("password", c.Password)
	add("dbname", c.Database)
	// Labels our connections in pg_stat_activity (see ActiveConnections).
	add("application_name", appName)
	return strings.Join(parts, " ")
}

// quoteDSNValue single-quotes a DSN value, escaping backslashes and quotes.
func quoteDSNValue(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}
