package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"
)

const connectTimeout = 5 * time.Second

// DBService holds the open connection between Connect and Disconnect.
type DBService struct {
	// queryMu lets one query run at a time on editor: a single Postgres
	// connection can't run two at once. Always lock queryMu before mu.
	queryMu sync.Mutex

	// mu guards the fields below. Wails runs each frontend call on its own
	// goroutine, so two calls can touch them at the same time.
	mu          sync.Mutex
	pool        *pgxpool.Pool
	editor      *pgxpool.Conn // the query editor's connection, held until Disconnect
	info        *ConnectionInfo
	cancelQuery context.CancelFunc // stops the running query, if any
	lastRun     *storedRun         // rows of the latest RunQuery, for FetchRows
	runSeq      int                // last RunID handed out
}

// ConnectionInfo describes an open (or just tested) connection.
type ConnectionInfo struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Database      string `json:"database"`
	ServerVersion string `json:"serverVersion"`
}

// ServerConnections is the list of client connections on the server.
type ServerConnections struct {
	MaxConnections int                `json:"maxConnections"`
	Connections    []ServerConnection `json:"connections"`
}

// ServerConnection is one row of pg_stat_activity.
type ServerConnection struct {
	PID              int    `json:"pid"`
	User             string `json:"user"`
	Database         string `json:"database"`
	Client           string `json:"client"` // "host:port", "local socket", or "" if hidden
	Application      string `json:"application"`
	State            string `json:"state"` // active, idle, idle in transaction, ...
	ConnectedSeconds int    `json:"connectedSeconds"`
	Query            string `json:"query"`     // current query, or the last one if idle
	IsCurrent        bool   `json:"isCurrent"` // the connection running this list query
}

// Client backends only (no autovacuum, checkpointer, ...). Without superuser or
// pg_read_all_stats, other users' rows show NULLs or "<insufficient privilege>".
const activityQuery = `
SELECT pid,
       coalesce(usename, ''),
       coalesce(datname, ''),
       CASE WHEN client_port = -1 THEN 'local socket'
            WHEN client_addr IS NULL THEN ''
            ELSE host(client_addr) || ':' || client_port END,
       coalesce(application_name, ''),
       coalesce(state, ''),
       coalesce(extract(epoch FROM now() - backend_start)::int, 0),
       coalesce(left(query, 500), ''),
       pid = pg_backend_pid()
FROM pg_stat_activity
WHERE backend_type = 'client backend'
ORDER BY pid = pg_backend_pid() DESC, coalesce(state, '') = 'active' DESC, backend_start`

// DefaultConfig returns the values that pre-fill the connection form.
func (s *DBService) DefaultConfig() (DBConfig, error) {
	return loadDBConfig()
}

// TestConnection opens a connection with cfg, runs SELECT 1 and closes it again.
// A nil error means the database is reachable; the error text is shown in the UI.
func (s *DBService) TestConnection(cfg DBConfig) (string, error) {
	if err := cfg.validate(); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, cfg.connString())
	if err != nil {
		return "", &ConnectionError{Summary: describeConnectError(err, cfg), Detail: err.Error()}
	}
	defer conn.Close(context.Background())

	info, err := selectOne(ctx, conn)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Connected to %s on %s:%d as %s", info.Database, info.Host, info.Port, info.User), nil
}

// Connect opens a connection pool with cfg and keeps it open until Disconnect.
// It also takes one connection out of the pool for the query editor, so every
// RunQuery uses the same session. Any connection already open is closed first.
func (s *DBService) Connect(cfg DBConfig) (ConnectionInfo, error) {
	if err := cfg.validate(); err != nil {
		return ConnectionInfo{}, err
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.connString())
	if err != nil {
		return ConnectionInfo{}, err
	}
	// The pool connects lazily; MinConns keeps one connection open while idle.
	poolCfg.MinConns = 1
	// When a query's context is cancelled, ask Postgres to stop the query so
	// the connection stays usable. pgx's default closes the connection instead.
	poolCfg.ConnConfig.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: 5 * time.Second}
	}

	// Stop a running query, then hold queryMu so none starts while we swap connections.
	s.stopRunningQuery()
	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return ConnectionInfo{}, err
	}
	editor, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return ConnectionInfo{}, &ConnectionError{Summary: describeConnectError(err, cfg), Detail: err.Error()}
	}
	info, err := selectOne(ctx, editor.Conn())
	if err != nil {
		editor.Release()
		pool.Close()
		return ConnectionInfo{}, err
	}

	s.pool = pool
	s.editor = editor
	s.info = &info
	return info, nil
}

// Disconnect stops a running query and closes the open connection. It does
// nothing if none is open.
func (s *DBService) Disconnect() {
	s.stopRunningQuery()
	s.queryMu.Lock() // wait for the stopped query to return
	defer s.queryMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

// stopRunningQuery cancels the running query, if any, without waiting for it.
func (s *DBService) stopRunningQuery() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelQuery != nil {
		s.cancelQuery()
	}
}

// ActiveConnection returns the open connection, or nil when disconnected. The
// frontend calls it on load, since a page reload doesn't reset the Go side.
func (s *DBService) ActiveConnection() *ConnectionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info == nil {
		return nil
	}
	info := *s.info
	return &info
}

// ActiveConnections lists the server's client connections. When connected it
// uses the open pool and ignores cfg; otherwise, like TestConnection, it opens
// a temporary connection with cfg and closes it afterwards.
func (s *DBService) ActiveConnections(cfg DBConfig) (ServerConnections, error) {
	// Hold the lock only to read s.pool. The pool itself is safe to use from
	// many goroutines, so the query runs without blocking Connect/Disconnect.
	// If Disconnect closes the pool meanwhile, the query returns an error.
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	if pool != nil {
		return listConnections(ctx, pool)
	}

	if err := cfg.validate(); err != nil {
		return ServerConnections{}, err
	}
	conn, err := pgx.Connect(ctx, cfg.connString())
	if err != nil {
		return ServerConnections{}, &ConnectionError{Summary: describeConnectError(err, cfg), Detail: err.Error()}
	}
	defer conn.Close(context.Background())
	return listConnections(ctx, conn)
}

// querier is the part of *pgxpool.Pool and *pgx.Conn that listConnections uses.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func listConnections(ctx context.Context, q querier) (ServerConnections, error) {
	result := ServerConnections{Connections: []ServerConnection{}}
	if err := q.QueryRow(ctx, "SELECT current_setting('max_connections')::int").Scan(&result.MaxConnections); err != nil {
		return ServerConnections{}, err
	}

	rows, err := q.Query(ctx, activityQuery)
	if err != nil {
		return ServerConnections{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ServerConnection
		if err := rows.Scan(&c.PID, &c.User, &c.Database, &c.Client, &c.Application,
			&c.State, &c.ConnectedSeconds, &c.Query, &c.IsCurrent); err != nil {
			return ServerConnections{}, err
		}
		result.Connections = append(result.Connections, c)
	}
	if err := rows.Err(); err != nil {
		return ServerConnections{}, err
	}
	return result, nil
}

// ServiceShutdown is called by Wails when the app quits.
func (s *DBService) ServiceShutdown() error {
	s.Disconnect()
	return nil
}

// closeLocked returns the editor connection and closes the pool. The caller
// must hold queryMu (so no query is using editor) and mu.
func (s *DBService) closeLocked() {
	if s.editor != nil {
		s.editor.Release()
	}
	if s.pool != nil {
		s.pool.Close()
	}
	s.editor = nil
	s.pool = nil
	s.info = nil
	s.lastRun = nil
}

// selectOne runs SELECT 1 on conn and describes the connection.
func selectOne(ctx context.Context, conn *pgx.Conn) (ConnectionInfo, error) {
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return ConnectionInfo{}, &ConnectionError{Summary: "Connected, but SELECT 1 failed.", Detail: err.Error()}
	}
	pg := conn.Config()
	return ConnectionInfo{
		Host:          pg.Host,
		Port:          int(pg.Port),
		User:          pg.User,
		Database:      pg.Database,
		ServerVersion: conn.PgConn().ParameterStatus("server_version"),
	}, nil
}
