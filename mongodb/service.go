// Package mongodb is SpyDB's MongoDB side: connecting, listing databases
// and collections, pages of documents, saving edits, and the query editor's
// shell-like commands. The app's screens call it through Service.
package mongodb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Service holds the open connection between Connect and Disconnect.
type Service struct {
	// mu guards the fields below. Wails runs each frontend call on its own
	// goroutine. Calls copy what they need under the lock and talk to the
	// server after releasing it, so Disconnect never waits for a slow one.
	mu     sync.Mutex
	client *mongo.Client
	info   *ConnectionInfo
	cfg    Config          // the open connection's settings, for Recent
	txns   bool            // the server can run transactions (a replica set or sharded cluster), so a save is all or nothing
	runs   map[string]*run // each query tab's latest result still being read (query.go)
	runSeq int             // last RunID handed out

	fileMu sync.Mutex // guards mongo-connections.json (see connections.go)
}

// ConnectionInfo describes the open (or just tested) connection.
type ConnectionInfo struct {
	Host          string `json:"host"`          // "localhost:27017", or a cluster's hosts
	User          string `json:"user"`          // "" when connected without signing in
	Database      string `json:"database"`      // the database the sidebar lists and the query editor's db is
	ServerVersion string `json:"serverVersion"` // e.g. "8.2.7"
	Topology      string `json:"topology"`      // "standalone", "replica set rs0" or "sharded cluster"
}

// CollectionInfo is one collection (or view) of the open database.
type CollectionInfo struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`  // "collection", "view" or "timeseries"
	Count *int64 `json:"count"` // about how many documents (from the collection's metadata, no scan); nil for views or when unknown
}

// DefaultConfig is what the connection form starts with.
func (s *Service) DefaultConfig() Config {
	return defaultConfig
}

// TestConnection connects with cfg, asks the server who it is and
// disconnects again. The message says where it got.
func (s *Service) TestConnection(cfg Config) (string, error) {
	if err := cfg.validate(); err != nil {
		return "", err
	}
	client, info, _, err := open(cfg)
	if err != nil {
		return "", err
	}
	defer client.Disconnect(context.Background())
	who := ""
	if info.User != "" {
		who = " as " + info.User
	}
	return fmt.Sprintf("Connected to MongoDB %s (%s) on %s%s", info.ServerVersion, info.Topology, info.Host, who), nil
}

// Connect opens a connection with cfg and keeps it until Disconnect. The
// new one is opened before the open one (if any) closes, so a failed
// Connect leaves things as they were. A successful one goes in Recent.
func (s *Service) Connect(cfg Config) (ConnectionInfo, error) {
	if err := cfg.validate(); err != nil {
		return ConnectionInfo{}, err
	}
	client, info, txns, err := open(cfg)
	if err != nil {
		return ConnectionInfo{}, err
	}
	if info.Database == "" {
		info.Database = firstDatabase(client)
	}
	s.mu.Lock()
	old := s.client
	s.client, s.info, s.cfg, s.txns = client, &info, cfg, txns
	s.closeRunsLocked()
	s.mu.Unlock()
	if old != nil {
		go old.Disconnect(context.Background())
	}
	s.recordRecent(cfg)
	return info, nil
}

// open connects with cfg and checks the server answers.
func open(cfg Config) (*mongo.Client, ConnectionInfo, bool, error) {
	client, err := mongo.Connect(cfg.clientOptions())
	if err != nil {
		return nil, ConnectionInfo{}, false, connectError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout+time.Second)
	defer cancel()
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		client.Disconnect(context.Background())
		return nil, ConnectionInfo{}, false, connectError(err)
	}
	// hello needs no sign-in; ping with the database checks the user and password.
	hosts, user, database := cfg.target()
	if err := client.Database(databaseOr(database, "admin")).RunCommand(ctx, bson.D{{Key: "ping", Value: 1}}).Err(); err != nil {
		client.Disconnect(context.Background())
		return nil, ConnectionInfo{}, false, connectError(err)
	}
	var build struct {
		Version string `bson:"version"`
	}
	_ = client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&build)
	info := ConnectionInfo{Host: hosts, User: user, Database: database, ServerVersion: build.Version, Topology: "standalone"}
	switch {
	case hello.SetName != "":
		info.Topology = "replica set " + hello.SetName
	case hello.Msg == "isdbgrid":
		info.Topology = "sharded cluster"
	}
	return client, info, info.Topology != "standalone", nil
}

func databaseOr(name, fallback string) string {
	if name == "" {
		return fallback
	}
	return name
}

// systemDatabases are MongoDB's own; SpyDB opens one only when asked.
var systemDatabases = []string{"admin", "config", "local"}

// firstDatabase is the database to open when none was asked for: the first
// of yours, A to Z, else "test" (mongosh's default).
func firstDatabase(client *mongo.Client) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	names, err := client.ListDatabaseNames(ctx, bson.D{}, options.ListDatabases().SetAuthorizedDatabases(true))
	if err != nil {
		return "test"
	}
	slices.Sort(names)
	for _, name := range names {
		if !slices.Contains(systemDatabases, name) {
			return name
		}
	}
	return "test"
}

// connectError says what went wrong connecting, in plain words where it can.
func connectError(err error) error {
	text := err.Error()
	switch {
	case strings.Contains(text, "server selection timeout") || strings.Contains(text, "connection refused"):
		return fmt.Errorf("Couldn’t reach a MongoDB server there: check the host and port, and that the server is running. (%s)", shorten(text, 300))
	case strings.Contains(text, "Authentication failed") || strings.Contains(text, "auth error"):
		return fmt.Errorf("MongoDB didn’t accept the user and password (check the auth database too). (%s)", shorten(text, 300))
	case strings.Contains(text, "no such host"):
		return fmt.Errorf("There’s no server with that name. (%s)", shorten(text, 300))
	}
	return err
}

// SwitchDatabase makes another database of the same server the open one:
// the sidebar lists its collections and the query editor's db is it.
func (s *Service) SwitchDatabase(name string) (ConnectionInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ConnectionInfo{}, errors.New("Pick a database.")
	}
	if err := validDatabase(name); err != nil {
		return ConnectionInfo{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info == nil {
		return ConnectionInfo{}, errors.New("Not connected to MongoDB.")
	}
	info := *s.info
	info.Database = name
	s.info = &info
	return info, nil
}

// ListDatabases lists the server's databases (the ones this user may use), A to Z.
func (s *Service) ListDatabases(ctx context.Context) ([]string, error) {
	client, _, err := s.connected()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	names, err := client.ListDatabaseNames(ctx, bson.D{}, options.ListDatabases().SetAuthorizedDatabases(true))
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
}

// ListCollections lists the open database's collections and views, A to Z,
// each collection with its document count from the server's metadata
// (estimatedDocumentCount: no documents are read).
func (s *Service) ListCollections(ctx context.Context) ([]CollectionInfo, error) {
	client, info, err := s.connected()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db := client.Database(info.Database)
	specs, err := db.ListCollectionSpecifications(ctx, bson.D{}, options.ListCollections().SetAuthorizedCollections(true))
	if err != nil {
		return nil, err
	}
	out := make([]CollectionInfo, len(specs))
	for i, spec := range specs {
		out[i] = CollectionInfo{Name: spec.Name, Kind: spec.Type}
	}
	slices.SortFunc(out, func(a, b CollectionInfo) int { return strings.Compare(a.Name, b.Name) })

	// Counts in parallel, a few at a time; one that fails or is slow stays unknown.
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i := range out {
		if out[i].Kind == "view" {
			continue
		}
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
			defer ccancel()
			if n, err := db.Collection(out[i].Name).EstimatedDocumentCount(cctx); err == nil {
				out[i].Count = &n
			}
		})
	}
	wg.Wait()
	return out, nil
}

// Disconnect closes the connection.
func (s *Service) Disconnect() {
	s.mu.Lock()
	client := s.client
	s.client, s.info = nil, nil
	s.closeRunsLocked()
	s.mu.Unlock()
	if client != nil {
		client.Disconnect(context.Background())
	}
}

// ActiveConnection is the open connection, or nil: a reloaded window picks it up.
func (s *Service) ActiveConnection() *ConnectionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info == nil {
		return nil
	}
	info := *s.info
	return &info
}

// ServiceShutdown closes the connection when SpyDB quits.
func (s *Service) ServiceShutdown() error {
	s.Disconnect()
	return nil
}

// connected is the open client and connection, or an error if none.
func (s *Service) connected() (*mongo.Client, ConnectionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || s.info == nil {
		return nil, ConnectionInfo{}, errors.New("Not connected to MongoDB: connect first.")
	}
	return s.client, *s.info, nil
}

// collection is the open database's collection by name.
func (s *Service) collection(name string) (*mongo.Collection, ConnectionInfo, error) {
	client, info, err := s.connected()
	if err != nil {
		return nil, ConnectionInfo{}, err
	}
	if name == "" {
		return nil, ConnectionInfo{}, errors.New("Pick a collection.")
	}
	return client.Database(info.Database).Collection(name), info, nil
}
