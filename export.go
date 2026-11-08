package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/xuri/excelize/v2"
)

// Excel's limits: rows per sheet and characters per cell.
const (
	excelMaxRows      = 1_048_576
	excelMaxCellChars = 32_767
)

// ChooseExportFile shows the Save dialog, starting in Downloads with
// suggestedName. format is "csv" or "xlsx". It returns "" if the user cancels.
func (s *DBService) ChooseExportFile(suggestedName, format string) (string, error) {
	ext, label, err := exportFormat(format)
	if err != nil {
		return "", err
	}
	dialog := application.Get().Dialog.SaveFile().
		SetFilename(suggestedName+ext).
		AddFilter(label, "*"+ext).
		CanCreateDirectories(true)
	if home, err := os.UserHomeDir(); err == nil {
		dialog.SetDirectory(filepath.Join(home, "Downloads"))
	}
	path, err := dialog.PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(path), ext) {
		path += ext
	}
	return path, nil
}

// RevealFile shows a file in Finder.
func (s *DBService) RevealFile(path string) error {
	return application.Get().Env.OpenFileManager(path, true)
}

// ExportResult writes one statement's result from a tab's latest run to path:
// exactly the rows the query returned. Results kept in full (up to
// maxStoredRows) are written from memory, without running anything again.
// A bigger single-statement result is run again in a READ ONLY transaction,
// so a query that changes data can never run twice, and streamed to the file.
func (s *DBService) ExportResult(ctx context.Context, tab string, runID, statement int, path, format string) (int, error) {
	s.mu.Lock()
	run := s.runs[tab]
	pool := s.pool
	s.mu.Unlock()
	if run == nil || run.id != runID {
		return 0, errors.New("These results are no longer available. Run the query again.")
	}
	if statement < 0 || statement >= len(run.rows) {
		return 0, errors.New("No such statement in this run.")
	}
	columns := run.columns[statement]
	categories := make([]string, len(columns))
	for i, c := range columns {
		categories[i] = resultCategory(c.Type)
	}

	w, err := newExportWriter(path, format, columnNames(columns), categories)
	if err != nil {
		return 0, err
	}

	// The whole result is in memory: write it as shown.
	if len(run.rows[statement]) == run.rowCounts[statement] {
		for _, row := range run.rows[statement] {
			if err := w.Write(row); err != nil {
				return 0, w.Abort(err)
			}
		}
		return w.Close()
	}

	// Too big to keep: run it again, read-only, and stream it.
	if len(run.rows) > 1 {
		return 0, w.Abort(errors.New("This result has more rows than SpyDB keeps, and it came from a run with several statements. Run that statement on its own, then export."))
	}
	if pool == nil {
		return 0, w.Abort(errors.New("Not connected."))
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, w.Abort(toQueryError(err))
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, run.sql, pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		return 0, w.Abort(rerunError(err))
	}
	n, err := streamRows(rows, w)
	if err != nil {
		return 0, rerunError(err)
	}
	return n, nil
}

// rerunError explains a refused re-run: Postgres reports a query that writes
// (even one hidden in a WITH) as "cannot execute … in a read-only transaction".
func rerunError(err error) error {
	var pgErr *pgconn.PgError
	var qe *QueryError
	readOnly := errors.As(err, &pgErr) && pgErr.Code == "25006" || // read_only_sql_transaction
		errors.As(err, &qe) && qe.Code == "25006" // the same, already converted while reading rows
	if readOnly {
		return &QueryError{Message: "This query changes data, so SpyDB won't run it a second time to export it. " +
			"Use a read-only query (a plain SELECT) to export more than 100,000 rows."}
	}
	return toQueryError(err)
}

// TableExport is a table tab's rows to export: the sort and filters as shown,
// all pages.
type TableExport struct {
	Schema   string        `json:"schema"`
	Table    string        `json:"table"`
	SortBy   string        `json:"sortBy"`
	SortDesc bool          `json:"sortDesc"`
	Filters  []TableFilter `json:"filters"`
}

// ExportTable streams every row of a table (matching the filters, in the
// chosen order) to path, using the pool. Nothing is held in memory.
func (s *DBService) ExportTable(ctx context.Context, e TableExport, path, format string) (int, error) {
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	if pool == nil {
		return 0, errors.New("Not connected.")
	}
	columns, err := tableColumns(ctx, pool, e.Schema, e.Table)
	if err != nil {
		return 0, err
	}
	sql, args, err := buildTableQuery(TableQuery{
		Schema: e.Schema, Table: e.Table, SortBy: e.SortBy, SortDesc: e.SortDesc, Filters: e.Filters,
	}, columns)
	if err != nil {
		return 0, err
	}

	names := make([]string, len(columns))
	categories := make([]string, len(columns))
	for i, c := range columns {
		names[i] = c.Name
		categories[i] = c.Category
	}
	w, err := newExportWriter(path, format, names, categories)
	if err != nil {
		return 0, err
	}
	rows, err := pool.Query(ctx, sql, append([]any{pgx.QueryResultFormats{pgx.TextFormatCode}}, args...)...)
	if err != nil {
		return 0, w.Abort(toQueryError(err))
	}
	return streamRows(rows, w)
}

// streamRows copies text-format rows into w, then finishes the file.
func streamRows(rows pgx.Rows, w exportWriter) (int, error) {
	defer rows.Close()
	for rows.Next() {
		values := rows.RawValues()
		row := make([]*string, len(values))
		for i, v := range values {
			if v != nil {
				text := string(v)
				row[i] = &text
			}
		}
		if err := w.Write(row); err != nil {
			return 0, w.Abort(err)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, w.Abort(toQueryError(err))
	}
	return w.Close()
}

// exportWriter writes rows to a file. Abort removes the partial file and
// returns err; Close finishes the file and returns how many rows it holds.
type exportWriter interface {
	Write(row []*string) error
	Close() (int, error)
	Abort(err error) error
}

func newExportWriter(path, format string, names, categories []string) (exportWriter, error) {
	switch format {
	case "csv":
		return newCSVWriter(path, names)
	case "xlsx":
		return newExcelWriter(path, names, categories)
	}
	_, _, err := exportFormat(format)
	return nil, err
}

func exportFormat(format string) (ext, label string, err error) {
	switch format {
	case "csv":
		return ".csv", "CSV", nil
	case "xlsx":
		return ".xlsx", "Excel workbook", nil
	}
	return "", "", fmt.Errorf("Unknown export format %q.", format)
}

// csvWriter: a header row, then one line per row. NULL becomes an empty field.
type csvWriter struct {
	path string
	file *os.File
	buf  *bufio.Writer
	csv  *csv.Writer
	rows int
}

func newCSVWriter(path string, names []string) (*csvWriter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	buf := bufio.NewWriter(file)
	w := &csvWriter{path: path, file: file, buf: buf, csv: csv.NewWriter(buf)}
	if err := w.csv.Write(names); err != nil {
		return nil, w.Abort(err)
	}
	return w, nil
}

func (w *csvWriter) Write(row []*string) error {
	record := make([]string, len(row))
	for i, v := range row {
		if v != nil {
			record[i] = *v
		}
	}
	w.rows++
	return w.csv.Write(record)
}

func (w *csvWriter) Close() (int, error) {
	w.csv.Flush()
	if err := w.csv.Error(); err != nil {
		return 0, w.Abort(err)
	}
	if err := w.buf.Flush(); err != nil {
		return 0, w.Abort(err)
	}
	if err := w.file.Close(); err != nil {
		os.Remove(w.path)
		return 0, err
	}
	return w.rows, nil
}

func (w *csvWriter) Abort(err error) error {
	w.file.Close()
	os.Remove(w.path)
	return err
}

// excelWriter streams rows into one sheet with a bold, frozen header row.
// Numbers and booleans are written as real numbers and booleans, so Excel can
// sum and sort them; everything else as text.
type excelWriter struct {
	path       string
	file       *excelize.File
	stream     *excelize.StreamWriter
	categories []string
	rows       int
}

func newExcelWriter(path string, names, categories []string) (*excelWriter, error) {
	file := excelize.NewFile()
	w := &excelWriter{path: path, file: file, categories: categories}
	stream, err := file.NewStreamWriter("Sheet1")
	if err != nil {
		return nil, w.Abort(err)
	}
	w.stream = stream
	bold, err := file.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return nil, w.Abort(err)
	}
	if err := stream.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return nil, w.Abort(err)
	}
	if len(names) > 0 {
		if err := stream.SetColWidth(1, len(names), 18); err != nil {
			return nil, w.Abort(err)
		}
	}
	header := make([]any, len(names))
	for i, name := range names {
		header[i] = excelize.Cell{StyleID: bold, Value: name}
	}
	if err := stream.SetRow("A1", header); err != nil {
		return nil, w.Abort(err)
	}
	return w, nil
}

func (w *excelWriter) Write(row []*string) error {
	if w.rows+1 >= excelMaxRows {
		return fmt.Errorf("Excel sheets hold at most %d rows. Export as CSV instead.", excelMaxRows)
	}
	values := make([]any, len(row))
	for i, v := range row {
		if v == nil {
			continue // an empty cell
		}
		values[i] = excelValue(*v, w.categories[i])
	}
	w.rows++
	cell, err := excelize.CoordinatesToCellName(1, w.rows+1)
	if err != nil {
		return err
	}
	return w.stream.SetRow(cell, values)
}

func (w *excelWriter) Close() (int, error) {
	if err := w.stream.Flush(); err != nil {
		return 0, w.Abort(err)
	}
	if err := w.file.SaveAs(w.path); err != nil {
		return 0, w.Abort(err)
	}
	w.file.Close()
	return w.rows, nil
}

func (w *excelWriter) Abort(err error) error {
	w.file.Close()
	os.Remove(w.path)
	return err
}

// excelValue turns a text value into what Excel should store. Numbers become
// numbers only if a float holds them exactly (no more than 15 significant
// digits); longer ones, like big IDs, stay text so no digit is lost.
func excelValue(v, category string) any {
	switch category {
	case "number":
		digits := strings.TrimLeft(strings.TrimPrefix(v, "-"), "0.")
		digits = strings.ReplaceAll(digits, ".", "")
		if f, err := strconv.ParseFloat(v, 64); err == nil && len(digits) <= 15 && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	case "bool":
		switch v {
		case "t":
			return true
		case "f":
			return false
		}
	}
	if len(v) > excelMaxCellChars {
		return v[:excelMaxCellChars]
	}
	return v
}

func columnNames(columns []Column) []string {
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = c.Name
	}
	return names
}

// resultCategory maps a result column's type name to a category, like the
// grid's typeCategory in the frontend.
func resultCategory(typ string) string {
	switch typ {
	case "int2", "int4", "int8", "numeric", "float4", "float8", "oid":
		return "number"
	case "bool":
		return "bool"
	}
	return "text"
}
