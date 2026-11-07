package main

import (
	"context"
	"errors"
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
}

// StatementResult is one statement's output. Statements that return no rows
// (INSERT, CREATE TABLE, ...) have no columns and only a command tag.
type StatementResult struct {
	Columns    []Column    `json:"columns"`
	Rows       [][]*string `json:"rows"`       // first page; values as text; nil is SQL NULL
	RowCount   int         `json:"rowCount"`   // rows the statement returned
	StoredRows int         `json:"storedRows"` // rows kept for paging (at most maxStoredRows)
	Command    string      `json:"command"`    // e.g. "SELECT 3", "UPDATE 1", "CREATE TABLE"
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"` // e.g. "int4", "text"; empty if unknown
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
	id   int
	rows [][][]*string
}

// statementData is one statement's full output before it is split into pages.
type statementData struct {
	columns  []Column
	rows     [][]*string // at most maxStoredRows
	rowCount int
	command  string
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
	if editor.Conn().IsClosed() {
		return QueryResult{}, &QueryError{Message: "The connection was lost. Disconnect and connect again."}
	}

	started := time.Now()
	statements, err := execute(ctx, editor.Conn(), sql)
	if err != nil {
		return QueryResult{}, toQueryError(err)
	}
	durationMs := int(time.Since(started).Milliseconds())

	run := &storedRun{}
	results := []StatementResult{}
	for _, st := range statements {
		run.rows = append(run.rows, st.rows)
		results = append(results, StatementResult{
			Columns:    st.columns,
			Rows:       page(st.rows, 0),
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

	return QueryResult{RunID: run.id, PageSize: pageSize, Results: results, DurationMs: durationMs}, nil
}

// FetchRows returns one page of a statement's rows from a tab's latest run,
// starting at offset. The rows are already in memory; nothing goes to Postgres.
func (s *DBService) FetchRows(tab string, runID, statement, offset int) ([][]*string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[tab]
	if run == nil || run.id != runID {
		return nil, errors.New("These results are no longer available. Run the query again.")
	}
	if statement < 0 || statement >= len(run.rows) {
		return nil, errors.New("No such statement in this run.")
	}
	return page(run.rows[statement], offset), nil
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
