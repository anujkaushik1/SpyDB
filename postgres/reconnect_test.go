package postgres

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The query editor's connection cut while idle: the next Run opens a new
// one, and runs on it only if nothing set on the old one is lost.

// cutEditor ends the editor's session from the server's side, as an idle
// timeout or a restart does, and waits until it has ended. The editor only
// finds out when it next talks to the server.
func cutEditor(t *testing.T, s *DBService) {
	t.Helper()
	var ended bool
	err := s.pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 5000)", int(s.editor.Conn().PgConn().PID())).Scan(&ended)
	if err != nil || !ended {
		t.Fatalf("pg_terminate_backend: %v, %v", ended, err)
	}
}

func runIn(t *testing.T, s *DBService, sql string) (QueryResult, error) {
	t.Helper()
	return s.RunQuery(t.Context(), "reconnect", sql)
}

func TestReconnectRuns(t *testing.T) {
	s := connectForTest(t)
	old := s.editor.Conn().PgConn().PID()
	if _, err := runIn(t, s, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	cutEditor(t, s)

	res, err := runIn(t, s, "SELECT 2")
	if err != nil {
		t.Fatalf("after the cut: %v", err)
	}
	if !res.Reconnected || *res.Results[0].Rows[0][0] != "2" {
		t.Errorf("got reconnected %v, rows %v; want true, 2", res.Reconnected, res.Results[0].Rows)
	}
	if s.editor.Conn().PgConn().PID() == old {
		t.Error("still the old connection")
	}
	if res, err := runIn(t, s, "SELECT 3"); err != nil || res.Reconnected {
		t.Errorf("next run: reconnected %v, %v; want false, nil", res.Reconnected, err)
	}
}

// Sessions that hold nothing a new connection lacks: the query runs.
func TestReconnectCleanSessions(t *testing.T) {
	s := connectForTest(t)
	for _, setup := range []string{
		"SELECT 1",
		"SET search_path = pg_catalog; RESET search_path",
		"SET statement_timeout = '5s'; RESET ALL",
		"BEGIN; SELECT 1; COMMIT",
		"BEGIN; SET LOCAL search_path = pg_catalog; COMMIT",
		"BEGIN; SELECT 1; ROLLBACK",
		"SELECT 1/0", // failed, outside a transaction
	} {
		runIn(t, s, setup)
		cutEditor(t, s)
		res, err := runIn(t, s, "SELECT 1")
		if err != nil || !res.Reconnected {
			t.Errorf("after %q: reconnected %v, %v; want it to run on a new connection", setup, res.Reconnected, err)
		}
	}
}

// Sessions that hold something a new connection lacks: the query doesn't
// run (it could act on another schema, role or table), and the next does.
func TestReconnectKeepsWhatWasLost(t *testing.T) {
	s := connectForTest(t)
	cases := []struct{ setup, want string }{
		{"SET search_path = pg_catalog", "is gone"},
		{"SELECT set_config('statement_timeout', '5s', false)", "is gone"},
		{"CREATE TEMP TABLE spydb_reconnect_probe AS SELECT 1", "is gone"},
		{"BEGIN", "rolled back"},
		{"BEGIN; SELECT 1/0", "rolled back"}, // a failed transaction
	}
	var member bool
	if err := s.pool.QueryRow(t.Context(), "SELECT pg_has_role('pg_monitor', 'MEMBER')").Scan(&member); err != nil {
		t.Fatal(err)
	}
	if member {
		cases = append(cases,
			struct{ setup, want string }{"SET ROLE pg_monitor", "is gone"},
			struct{ setup, want string }{"SET SESSION AUTHORIZATION pg_monitor", "is gone"})
	} else {
		t.Log("not a member of pg_monitor: SET ROLE / SET SESSION AUTHORIZATION not tried")
	}

	for _, c := range cases {
		runIn(t, s, c.setup)
		cutEditor(t, s)
		_, err := runIn(t, s, "SELECT current_user, current_setting('search_path')")
		if err == nil || !strings.Contains(err.Error(), "Your query didn't run: ") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("after %q: got %v; want it not run, saying %q", c.setup, err, c.want)
			continue
		}
		// The next Run runs, on a session as Connect opens it.
		res, err := runIn(t, s, "SELECT current_user = session_user, current_setting('search_path')")
		if err != nil || res.Reconnected {
			t.Errorf("after %q, the next run: reconnected %v, %v", c.setup, res.Reconnected, err)
			continue
		}
		if row := res.Results[0].Rows[0]; *row[0] != "t" || *row[1] != `"$user", public` {
			t.Errorf("after %q, the new session has same user %s, search_path %s", c.setup, *row[0], *row[1])
		}
	}
}

// A connection cut while a query ran: the query may or may not have run,
// and the error says so; the next Run reconnects.
func TestReconnectAfterCutMidRun(t *testing.T) {
	s := connectForTest(t)
	_, err := runIn(t, s, "SELECT pg_terminate_backend(pg_backend_pid())")
	if err == nil || !strings.Contains(err.Error(), lostWhileRunning) {
		t.Fatalf("got %v; want it to say %q", err, lostWhileRunning)
	}
	if res, err := runIn(t, s, "SELECT 1"); err != nil || !res.Reconnected {
		t.Errorf("next run: reconnected %v, %v; want true, nil", res.Reconnected, err)
	}

	// What was set before that run is still reported as lost.
	runIn(t, s, "SET search_path = pg_catalog")
	runIn(t, s, "SELECT pg_terminate_backend(pg_backend_pid())")
	if _, err := runIn(t, s, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Errorf("after SET and a cut mid-run: %v; want it not run", err)
	}
}

// Cancel doesn't stop SpyDB from noting the session: a clean one stays clean.
func TestReconnectAfterCancel(t *testing.T) {
	s := connectForTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if _, err := s.RunQuery(ctx, "reconnect", "SELECT pg_sleep(10)"); err == nil || err.Error() != "Query cancelled." {
		t.Fatalf("got %v; want Query cancelled.", err)
	}
	cutEditor(t, s)
	if res, err := runIn(t, s, "SELECT 1"); err != nil || !res.Reconnected {
		t.Errorf("reconnected %v, %v; want true, nil", res.Reconnected, err)
	}
}

// A network change can leave the connections silent: nothing arrives,
// nothing closes. The check gives up after pingTimeout, and the query runs
// on a new connection, not on another silent one from the pool. Disconnect
// doesn't wait for pgx to finish closing the silent ones.
func TestReconnectSilentConnection(t *testing.T) {
	database := os.Getenv("SPYDB_TEST_DATABASE")
	if database == "" {
		t.Skip("set SPYDB_TEST_DATABASE to run tests against Postgres")
	}
	isolate(t)
	host := os.Getenv("SPYDB_TEST_HOST")
	if host == "" {
		host = "localhost"
	}
	proxy := newSilencer(t, net.JoinHostPort(host, "5432"))
	s := &DBService{}
	if _, err := s.Connect(DBConfig{Host: "127.0.0.1", Port: proxy.port(), User: os.Getenv("SPYDB_TEST_USER"), Database: database}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Disconnect)

	proxy.silenceAll() // the connections open now: the editor's and the pool's

	// No new connection can be opened either: it says so, and the next Run tries again.
	proxy.refuse.Store(true)
	if _, err := runIn(t, s, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "a new one couldn't be opened") {
		t.Fatalf("got %v; want it to say no new connection could be opened", err)
	}
	proxy.refuse.Store(false)

	started := time.Now()
	res, err := runIn(t, s, "SELECT 1")
	took := time.Since(started)
	if err != nil || !res.Reconnected {
		t.Fatalf("reconnected %v, %v; want true, nil", res.Reconnected, err)
	}
	t.Logf("ran on a new connection after %v", took.Round(time.Millisecond))
	if took > 2*time.Second { // known lost already: no wait for the check
		t.Errorf("took %v", took)
	}

	started = time.Now()
	s.Disconnect()
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("Disconnect took %v", took)
	}

	// Disconnect while the editor's connection is lost and not yet replaced.
	s = &DBService{}
	if _, err := s.Connect(DBConfig{Host: "127.0.0.1", Port: proxy.port(), User: os.Getenv("SPYDB_TEST_USER"), Database: database}); err != nil {
		t.Fatal(err)
	}
	proxy.silenceAll()
	proxy.refuse.Store(true)
	runIn(t, s, "SELECT 1")
	started = time.Now()
	s.Disconnect()
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("Disconnect with the connection lost took %v", took)
	}
}

// silencer forwards TCP connections to Postgres; silenceAll makes the ones
// open so far drop everything, without closing them. While refuse is set,
// new connections are closed at once.
type silencer struct {
	ln     net.Listener
	refuse atomic.Bool
	mu     sync.Mutex
	conns  []*atomic.Bool // per connection: silenced
}

func newSilencer(t *testing.T, target string) *silencer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &silencer{ln: ln}
	var open []net.Conn
	t.Cleanup(func() {
		ln.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, c := range open {
			c.Close()
		}
	})
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			if p.refuse.Load() {
				client.Close()
				continue
			}
			server, err := net.Dial("tcp", target)
			if err != nil {
				client.Close()
				continue
			}
			silenced := &atomic.Bool{}
			p.mu.Lock()
			p.conns = append(p.conns, silenced)
			open = append(open, client, server)
			p.mu.Unlock()
			go forward(server, client, silenced)
			go forward(client, server, silenced)
		}
	}()
	return p
}

func (p *silencer) port() int { return p.ln.Addr().(*net.TCPAddr).Port }

func (p *silencer) silenceAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.conns {
		s.Store(true)
	}
}

// forward copies src to dst until either closes, then closes both, as a
// real network passes a close on.
func forward(dst, src net.Conn, silenced *atomic.Bool) {
	defer dst.Close()
	defer src.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		if !silenced.Load() {
			dst.Write(buf[:n])
		}
	}
}
