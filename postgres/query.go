package postgres

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// pageSize is how many rows go to the screen at a time.
	pageSize = 1000
	// maxStoredRows caps the rows kept in Go per statement for paging, so a
	// SELECT on a huge table can't use unbounded memory.
	maxStoredRows = 100_000
)

// QueryResult is what one RunQuery returns: a result per statement.
type QueryResult struct {
	RunID      int               `json:"runId"`    // pass to FetchRows for later pages
	PageSize   int               `json:"pageSize"` // rows per page
	Results    []StatementResult `json:"results"`
	DurationMs int               `json:"durationMs"`
	// The editor's connection had been lost, and this ran on a new one.
	Reconnected bool `json:"reconnected"`
}

// StatementResult is one statement's output. Statements that return no rows
// (INSERT, CREATE TABLE, ...) have no columns and only a command tag.
type StatementResult struct {
	Columns    []Column    `json:"columns"`
	Target     *EditTarget `json:"target"`     // where edited cells save to; nil means read-only
	ReadOnly   string      `json:"readOnly"`   // why it's read-only, e.g. "a join"
	Note       string      `json:"note"`       // what SpyDB changed in the SQL, e.g. "RETURNING * added by SpyDB"
	Rows       [][]*string `json:"rows"`       // first page; values as text; nil is SQL NULL
	Keys       [][]*string `json:"keys"`       // per row of Rows, its key (Target.Key); empty when read-only
	RowCount   int         `json:"rowCount"`   // rows the statement returned
	StoredRows int         `json:"storedRows"` // rows kept for paging (at most maxStoredRows)
	Command    string      `json:"command"`    // e.g. "SELECT 3", "UPDATE 1", "CREATE TABLE"
}

type Column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`       // e.g. "int4", "text"; empty if unknown
	Source     string `json:"source"`     // the table column it edits, when the result is editable
	PrimaryKey bool   `json:"primaryKey"` // part of the target table's primary key
}

// QueryError is returned when a query fails. Wails JSON-encodes it into the
// rejected promise's `cause`, so the UI can show the hint and error position.
type QueryError struct {
	Message  string `json:"message"`
	Detail   string `json:"detail"`
	Hint     string `json:"hint"`
	Code     string `json:"code"`     // SQLSTATE, e.g. "42601" for a syntax error
	Position int    `json:"position"` // 1-based character in the SQL; 0 if unknown
}

func (e *QueryError) Error() string { return e.Message }

// storedRun holds every kept row of one RunQuery, per statement.
type storedRun struct {
	id        int
	sql       string            // the text that ran, for exporting a result too big to keep
	session   map[string]string // the editor session's settings then (sessionOf), for that export
	columns   [][]Column        // per statement
	rows      [][][]*string     // per statement, at most maxStoredRows each
	keys      [][][]*string     // per statement, each row's key when the result is editable, else nil
	rowCounts []int             // per statement: rows the statement returned
}

// statementData is one statement's full output before it is split into pages.
type statementData struct {
	columns   []Column
	tableOIDs []uint32    // per column: the table it comes from, 0 for expressions
	attNums   []uint16    // per column: its column number in that table
	rows      [][]*string // at most maxStoredRows
	rowCount  int
	command   string
}

// RunQuery runs sql on the editor connection opened by Connect and returns the
// first page of each statement's rows. tab names the query tab, so each tab
// keeps its own rows for FetchRows. All tabs share the one editor connection,
// so queries from different tabs take turns. Wails cancels ctx when the
// frontend cancels the call (the Cancel button); Disconnect cancels it too.
func (s *DBService) RunQuery(ctx context.Context, tab string, sql string) (QueryResult, error) {
	if strings.TrimSpace(sql) == "" {
		return QueryResult{}, &QueryError{Message: "Write a query first."}
	}

	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	// Cancelled while waiting for another tab's query: don't start.
	if ctx.Err() != nil {
		return QueryResult{}, toQueryError(ctx.Err())
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	editor := s.editor
	s.cancelQuery = cancel
	delete(s.runs, tab) // this run replaces the tab's previous rows
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancelQuery = nil
		s.mu.Unlock()
	}()

	if editor == nil {
		return QueryResult{}, &QueryError{Message: "Not connected. Click Connect first."}
	}
	// A connection cut while idle is replaced first (reconnect.go).
	conn, reconnected, err := s.liveEditor(ctx, editor)
	if err != nil {
		return QueryResult{}, err
	}
	defer s.noteSession(ctx, conn)

	// A plain single-table SELECT may run with its table's key added as
	// hidden columns (planEdit), so its rows can be edited; an UPDATE with
	// RETURNING * added (planReturning), so the rows it changed show.
	plan, toRun := planEdit(ctx, conn, sql)
	var returning returningPlan
	if plan.added == nil {
		returning, toRun = planReturning(ctx, conn, toRun)
	}
	started := time.Now()
	statements, err := execute(ctx, conn, toRun)
	if err != nil {
		qe := toQueryError(err)
		if plan.added != nil {
			qe.Position = plan.insertion.originalPosition(qe.Position)
		}
		qe.Position = returning.originalPosition(qe.Position)
		if conn.IsClosed() {
			qe.Message += " " + lostWhileRunning
		}
		return QueryResult{}, qe
	}
	durationMs := int(time.Since(started).Milliseconds())
	// Rows an UPDATE returned are editable only once committed: nothing is
	// left open, and nothing in the run rolled back (ROLLBACK, ABORT,
	// ROLLBACK TO SAVEPOINT and a failed COMMIT all say ROLLBACK), or their
	// keys can name other rows.
	rolledBack := slices.ContainsFunc(statements, func(st statementData) bool {
		return strings.HasPrefix(st.command, "ROLLBACK") || st.command == "PREPARE TRANSACTION"
	})
	committed := conn.PgConn().TxStatus() == 'I' && !rolledBack
	// Rows read in a transaction that has written may be its own uncommitted
	// changes: if it rolls back, their keys can name other rows.
	if plan.reason == "" && plan.inTransaction && conn.PgConn().TxStatus() == 'T' && wroteInTransaction(ctx, conn) {
		plan.reason = transactionWrote
	}
	// The session's role and formats, for saves (and exports that run again).
	var session map[string]string
	if plan.reason == "" && len(statements) > 0 && len(statements[len(statements)-1].columns) > 0 ||
		returning.added != nil && committed {
		session = sessionOf(ctx, conn)
	}

	// Work out which results can be edited. The catalog lookups use the pool,
	// so a failed transaction in the editor's session can't break them.
	s.mu.Lock()
	pool, generation := s.pool, s.generation
	s.mu.Unlock()

	run := &storedRun{sql: sql, session: session}
	results := []StatementResult{}
	updates := 0 // UPDATE statements so far, to spot the ones given RETURNING *
	for i := range statements {
		st := &statements[i]
		var target *EditTarget
		var keys [][]*string
		var readOnly, note string
		returned := false
		if strings.HasPrefix(st.command, "UPDATE ") {
			returned = returning.ordinals[updates]
			updates++
		}
		if returned {
			note = returningNote
		}
		if len(st.columns) > 0 {
			switch {
			case returned && rolledBack:
				readOnly = returningRolledBack
			case returned && !committed:
				readOnly = returningUncommitted
			case pool == nil:
				readOnly = "not connected"
			case returned:
				target, keys, readOnly = editPlan{returning: true}.resolve(ctx, pool, session, st)
			default:
				target, keys, readOnly = plan.resolve(ctx, pool, session, st)
			}
			if target != nil {
				target.Conn = generation
			}
		}
		run.rows = append(run.rows, st.rows)
		run.keys = append(run.keys, keys)
		run.columns = append(run.columns, st.columns)
		run.rowCounts = append(run.rowCounts, st.rowCount)
		results = append(results, StatementResult{
			Columns:    st.columns,
			Target:     target,
			ReadOnly:   readOnly,
			Note:       note,
			Rows:       page(st.rows, 0),
			Keys:       page(keys, 0),
			RowCount:   st.rowCount,
			StoredRows: len(st.rows),
			Command:    st.command,
		})
	}

	// Store the run only once it is complete, so FetchRows never sees it half-built.
	s.mu.Lock()
	s.runSeq++
	run.id = s.runSeq
	if s.runs == nil {
		s.runs = map[string]*storedRun{}
	}
	s.runs[tab] = run
	s.mu.Unlock()

	return QueryResult{RunID: run.id, PageSize: pageSize, Results: results, DurationMs: durationMs, Reconnected: reconnected}, nil
}

// ResultPage is one page of a statement's rows, with their keys when the
// result is editable.
type ResultPage struct {
	Rows [][]*string `json:"rows"`
	Keys [][]*string `json:"keys"`
}

// FetchRows returns one page of a statement's rows from a tab's latest run,
// starting at offset. The rows are already in memory; nothing goes to Postgres.
func (s *DBService) FetchRows(tab string, runID, statement, offset int) (ResultPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[tab]
	if run == nil || run.id != runID {
		return ResultPage{}, errors.New("These results are no longer available. Run the query again.")
	}
	if statement < 0 || statement >= len(run.rows) {
		return ResultPage{}, errors.New("No such statement in this run.")
	}
	return ResultPage{Rows: page(run.rows[statement], offset), Keys: page(run.keys[statement], offset)}, nil
}

// ForgetRun drops a tab's stored rows; the frontend calls it when a query tab closes.
func (s *DBService) ForgetRun(tab string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runs, tab)
}

// page returns up to pageSize rows starting at offset.
func page(rows [][]*string, offset int) [][]*string {
	if offset < 0 || offset >= len(rows) {
		return [][]*string{}
	}
	return rows[offset:min(offset+pageSize, len(rows))]
}

// execute runs sql with Postgres's simple query protocol, so one run can hold
// several statements (BEGIN; UPDATE ...; COMMIT;). Values come back as text.
func execute(ctx context.Context, conn *pgx.Conn, sql string) ([]statementData, error) {
	types := conn.TypeMap()
	mrr := conn.PgConn().Exec(ctx, sql)
	statements := []statementData{}

	for mrr.NextResult() {
		rr := mrr.ResultReader()
		st := statementData{columns: []Column{}, rows: [][]*string{}}
		for _, fd := range rr.FieldDescriptions() {
			col := Column{Name: fd.Name}
			if t, ok := types.TypeForOID(fd.DataTypeOID); ok {
				col.Type = t.Name
			}
			st.columns = append(st.columns, col)
			st.tableOIDs = append(st.tableOIDs, fd.TableOID)
			st.attNums = append(st.attNums, fd.TableAttributeNumber)
		}

		for rr.NextRow() {
			st.rowCount++
			if len(st.rows) >= maxStoredRows {
				continue // keep counting, stop storing
			}
			values := rr.Values() // reused by the next row, so copy each value
			row := make([]*string, len(values))
			for i, v := range values {
				if v != nil {
					text := string(v)
					row[i] = &text
				}
			}
			st.rows = append(st.rows, row)
		}

		tag, err := rr.Close()
		if err != nil {
			mrr.Close()
			return nil, err
		}
		st.command = tag.String()
		statements = append(statements, st)
	}

	if err := mrr.Close(); err != nil {
		return nil, err
	}
	return statements, nil
}

func toQueryError(err error) *QueryError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "57014" { // query_canceled
			return &QueryError{Message: "Query cancelled.", Code: pgErr.Code}
		}
		return &QueryError{
			Message:  sentence(pgErr.Message),
			Detail:   pgErr.Detail,
			Hint:     pgErr.Hint,
			Code:     pgErr.Code,
			Position: int(pgErr.Position),
		}
	}
	if errors.Is(err, context.Canceled) {
		return &QueryError{Message: "Query cancelled."}
	}
	return &QueryError{Message: sentence(err.Error())}
}

// wroteInTransaction reports whether the editor's open transaction has
// written or locked rows (it has a transaction ID), so rows read in it may be
// uncommitted. Before Postgres 10 there's no way to ask without assigning
// one, so it counts as yes; so does any error (the read can't fail the
// transaction: it only runs on a healthy one).
func wroteInTransaction(ctx context.Context, conn *pgx.Conn) bool {
	if serverMajor(conn.PgConn().ParameterStatus("server_version")) < 10 {
		return true
	}
	var xid *int64
	err := conn.QueryRow(ctx, "SELECT pg_catalog.txid_current_if_assigned()", pgx.QueryExecModeExec).Scan(&xid)
	return err != nil || xid != nil
}

// serverMajor is the major version in a server_version like "15.15",
// "9.6.24" or "17beta1"; 0 if unknown.
func serverMajor(version string) int {
	end := 0
	for end < len(version) && version[end] >= '0' && version[end] <= '9' {
		end++
	}
	major, _ := strconv.Atoi(version[:end])
	return major
}
