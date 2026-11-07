package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type DBService struct{}

// TestConnection connects with the settings in db.config.json and runs SELECT 1.
// A nil error means the database is reachable; the error text is shown in the UI.
func (s *DBService) TestConnection() (string, error) {
	cfg, err := loadDBConfig()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, cfg.connString())
	if err != nil {
		return "", err
	}
	defer conn.Close(context.Background())

	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return "", fmt.Errorf("SELECT 1 failed: %w", err)
	}

	return fmt.Sprintf("Connected to %s on %s:%d as %s", cfg.Database, cfg.Host, cfg.Port, cfg.User), nil
}
