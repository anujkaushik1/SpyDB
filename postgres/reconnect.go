package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The query editor keeps one connection from Connect to Disconnect (so SET
// and transactions carry over between runs). It can be cut while idle: the
// Mac slept, the network changed, the server closed idle sessions. RunQuery
// checks it first (liveEditor) and, if it's gone, opens a new one.

// pingTimeout limits the check that the editor's connection is alive.
// Postgres answers it at once; a connection cut by a network change may
// never answer.
const pingTimeout = 3 * time.Second

// What the editor's session held that a new connection doesn't have, as
// kept by noteSession; the query then doesn't run on the new connection.
const (
	lostTransaction = "the transaction open on the old connection was rolled back. Run it again when you're ready (from BEGIN, if it needs one)."
	lostSettings    = "what was set on the old connection (SET, SET ROLE, set_config, temporary tables) is gone. Run it again when you're ready (after setting it again, if it needs it)."
	// After an error that cut the connection mid-run.
	lostWhileRunning = "The connection to the server was lost while it ran, so it may or may not have run. The next Run opens a new connection."
)

// sessionChanged asks whether the session differs from a new connection's:
// a setting changed (SET, set_config), another role (SET ROLE, SET SESSION
// AUTHORIZATION; $1 is the user SpyDB logged in as) or a temporary table.
const sessionChanged = `SELECT EXISTS (SELECT FROM pg_catalog.pg_settings WHERE source = 'session')
	OR current_user <> session_user OR session_user <> $1 OR pg_catalog.pg_my_temp_schema() <> 0`

// liveEditor returns the editor's connection after checking it's alive. A
// cut one is swapped for a new connection from the pool; reconnected says
// so. The query runs on it only if the old session held nothing the new one
// lacks (noteSession); otherwise the error says what was lost, and the next
// Run runs. Call with queryMu held, so Connect and Disconnect wait.
func (s *DBService) liveEditor(ctx context.Context, editor *pgxpool.Conn) (conn *pgx.Conn, reconnected bool, err error) {
	conn = editor.Conn()
	if !conn.IsClosed() {
		// A deadline on the socket, not on ctx: a timed-out ctx makes pgx
		// ask the server to cancel and wait 5 more seconds for it.
		pg := conn.PgConn()
		pg.Conn().SetDeadline(time.Now().Add(pingTimeout))
		err := pg.Ping(ctx)
		pg.Conn().SetDeadline(time.Time{})
		if err == nil {
			return conn, false, nil
		}
	}
	if ctx.Err() != nil { // Cancel clicked during the check
		return nil, false, toQueryError(ctx.Err())
	}

	s.mu.Lock()
	pool, lost := s.pool, s.editorLoss
	s.mu.Unlock()
	// Whatever cut it (the network, the server) most likely cut the pool's
	// idle connections too: drop them all, so Acquire opens a new one
	// rather than handing out another dead one.
	pool.Reset()
	acquireCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	fresh, err := pool.Acquire(acquireCtx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, toQueryError(ctx.Err())
		}
		// The cut connection stays the editor's, so the next Run tries again.
		return nil, false, &QueryError{Message: "The connection to the server was lost, and a new one couldn't be opened: " +
			sentence(err.Error()) + " Check your network, then Run again."}
	}

	s.mu.Lock()
	s.editor, s.editorLoss = fresh, ""
	s.mu.Unlock()
	// Taken out of the pool, not handed back: pgx can take 15 seconds to
	// finish closing a silent connection, and the pool's Close (Disconnect)
	// would wait for it. A failed ping has closed it already (pgx closes on
	// any network error); this is in case one didn't.
	dead := editor.Hijack()
	if !dead.IsClosed() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		dead.Close(closeCtx)
		cancel()
	}

	if lost != "" {
		return nil, false, &QueryError{Message: "The connection to the server was lost, so SpyDB opened a new one. Your query didn't run: " + lost}
	}
	return fresh.Conn(), true, nil
}

// noteSession keeps, after a run, what conn's session holds that a new
// connection wouldn't (lostTransaction, lostSettings, or "" for nothing),
// for when it's lost later. If it's lost already, the last one stays.
func (s *DBService) noteSession(ctx context.Context, conn *pgx.Conn) {
	if conn.IsClosed() {
		return
	}
	loss := ""
	if conn.PgConn().TxStatus() != 'I' {
		loss = lostTransaction
	} else {
		// Not cancelled with the run: after Cancel, the session can still have changed.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pingTimeout)
		defer cancel()
		var changed bool
		err := conn.QueryRow(ctx, sessionChanged, pgx.QueryExecModeExec, conn.Config().User).Scan(&changed)
		if err != nil && conn.IsClosed() {
			return
		}
		if err != nil || changed { // can't tell: say it changed
			loss = lostSettings
		}
	}
	s.mu.Lock()
	s.editorLoss = loss
	s.mu.Unlock()
}
