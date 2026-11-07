package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// maxRows caps the rows kept per statement, so a big SELECT can't flood the UI.
const maxRows = 1000

// QueryResult is what one RunQuery returns: a result per statement.
type QueryResult struct {
	Results    []StatementResult `json:"results"`
	DurationMs int               `json:"durationMs"`
}

// StatementResult is one statement's output. Statements that return no rows
// (INSERT, CREATE TABLE, ...) have no columns and only a command tag.
type StatementResult struct {
	Columns  []Column    `json:"columns"`
	Rows     [][]*string `json:"rows"`     // values as text; nil is SQL NULL; at most maxRows
	RowCount int         `json:"rowCount"` // rows the statement returned, even past maxRows
	Command  string      `json:"command"`  // e.g. "SELECT 3", "UPDATE 1", "CREATE TABLE"
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

// RunQuery runs sql on the editor connection opened by Connect. Wails cancels
// ctx when the frontend cancels the call (the Cancel button); Disconnect
// cancels it too.
func (s *DBService) RunQuery(ctx context.Context, sql string) (QueryResult, error) {
	if strings.TrimSpace(sql) == "" {
		return QueryResult{}, &QueryError{Message: "Write a query first."}
	}

	s.queryMu.Lock()
	defer s.queryMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	editor := s.editor
	s.cancelQuery = cancel
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
	results, err := execute(ctx, editor.Conn(), sql)
	if err != nil {
		return QueryResult{}, toQueryError(err)
	}
	return QueryResult{Results: results, DurationMs: int(time.Since(started).Milliseconds())}, nil
}

// execute runs sql with Postgres's simple query protocol, so one run can hold
// several statements (BEGIN; UPDATE ...; COMMIT;). Values come back as text.
func execute(ctx context.Context, conn *pgx.Conn, sql string) ([]StatementResult, error) {
	types := conn.TypeMap()
	mrr := conn.PgConn().Exec(ctx, sql)
	results := []StatementResult{}

	for mrr.NextResult() {
		rr := mrr.ResultReader()
		res := StatementResult{Columns: []Column{}, Rows: [][]*string{}}
		for _, fd := range rr.FieldDescriptions() {
			col := Column{Name: fd.Name}
			if t, ok := types.TypeForOID(fd.DataTypeOID); ok {
				col.Type = t.Name
			}
			res.Columns = append(res.Columns, col)
		}

		for rr.NextRow() {
			res.RowCount++
			if len(res.Rows) >= maxRows {
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
			res.Rows = append(res.Rows, row)
		}

		tag, err := rr.Close()
		if err != nil {
			mrr.Close()
			return nil, err
		}
		res.Command = tag.String()
		results = append(results, res)
	}

	if err := mrr.Close(); err != nil {
		return nil, err
	}
	return results, nil
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
