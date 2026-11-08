package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/xuri/excelize/v2"
)

// Importing CSV and Excel files: into an existing table, or into a new one
// created from the file. The file is read twice: once for the preview (every
// row, to count them and suggest column types), once to stream it into
// Postgres with COPY. The import is one transaction: every row goes in, or
// none does (and a new table isn't created).

const importPreviewRows = 20

// ImportOptions says which file to read and how.
type ImportOptions struct {
	Path      string `json:"path"`
	Sheet     string `json:"sheet"`     // Excel: the sheet; "" for the first
	Delimiter string `json:"delimiter"` // CSV: the separator; "" to detect it
	HasHeader bool   `json:"hasHeader"` // the first row holds column names
}

// ImportColumn is one column of the file, with suggestions for a new table.
type ImportColumn struct {
	Header string `json:"header"` // as in the file ("" without a header row)
	Name   string `json:"name"`   // suggested column name
	Type   string `json:"type"`   // suggested Postgres type, from every value
	Empty  int    `json:"empty"`  // empty cells
}

// ImportPreview is what a file holds, read with ImportOptions.
type ImportPreview struct {
	Format    string         `json:"format"`    // csv or xlsx
	Sheets    []string       `json:"sheets"`    // Excel: every sheet
	Sheet     string         `json:"sheet"`     // Excel: the sheet read
	Delimiter string         `json:"delimiter"` // CSV: the separator used
	Columns   []ImportColumn `json:"columns"`
	Rows      [][]string     `json:"rows"`      // the first rows, as they'll be imported
	RowCount  int            `json:"rowCount"`  // data rows (not counting the header)
	TableName string         `json:"tableName"` // suggested name for a new table
	KeyColumn int            `json:"keyColumn"` // file column suggested as primary key, or -1
	Notes     []string       `json:"notes"`     // e.g. how the file was decoded
}

// ImportTargetColumn is a column of an existing table, for matching.
type ImportTargetColumn struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Required  bool   `json:"required"`  // NOT NULL with no default: every row needs a value
	Generated bool   `json:"generated"` // GENERATED ALWAYS (identity or computed): can't be filled
}

// ImportChoice is what happens to one file column.
type ImportChoice struct {
	Target string `json:"target"` // the table column it fills; "" skips it
	Type   string `json:"type"`   // new table only: the column's type
}

// ImportRequest is a whole import.
type ImportRequest struct {
	ImportOptions
	EmptyAsNull bool           `json:"emptyAsNull"` // empty cells become NULL (else empty text)
	Schema      string         `json:"schema"`
	Table       string         `json:"table"`
	NewTable    bool           `json:"newTable"`
	Choices     []ImportChoice `json:"choices"`   // one per file column
	AddKey      string         `json:"addKey"`    // new table: an id column to add as primary key, or ""
	KeyColumn   int            `json:"keyColumn"` // new table: file column to make the primary key, or -1
}

// ImportResult says what an import did.
type ImportResult struct {
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Rows       int    `json:"rows"`
	Created    bool   `json:"created"`
	DurationMs int    `json:"durationMs"`
}

// The types a new table's columns can have (the dialog offers these).
var importTypes = map[string]bool{
	"bigint": true, "integer": true, "numeric": true, "double precision": true, "boolean": true,
	"date": true, "timestamp": true, "timestamptz": true, "time": true, "text": true, "jsonb": true, "uuid": true,
}

// ChooseImportFile shows the Open dialog for a CSV or Excel file; "" if cancelled.
func (s *DBService) ChooseImportFile() (string, error) {
	dialog := application.Get().Dialog.OpenFile().
		SetTitle("Import a CSV or Excel file").
		CanChooseFiles(true).
		AddFilter("CSV or Excel", "*.csv;*.tsv;*.txt;*.xlsx")
	if home, err := os.UserHomeDir(); err == nil {
		dialog.SetDirectory(filepath.Join(home, "Downloads"))
	}
	return dialog.PromptForSingleSelection()
}

// PreviewImport reads the whole file: the first rows to show, the row
// count, and for a new table suggested names, types and a primary key.
func (s *DBService) PreviewImport(opts ImportOptions) (ImportPreview, error) {
	src, info, err := openImportRows(opts)
	if err != nil {
		return ImportPreview{}, err
	}
	defer src.Close()

	preview := ImportPreview{Format: info.format, Sheets: info.sheets, Sheet: info.sheet, Delimiter: info.delimiter,
		Rows: [][]string{}, KeyColumn: -1, Notes: info.notes}
	var header []string
	if opts.HasHeader {
		header, err = src.Read()
		if errors.Is(err, io.EOF) {
			return ImportPreview{}, errors.New("The file is empty.")
		}
		if err != nil {
			return ImportPreview{}, readError(err)
		}
	}
	var stats []columnStats
	idColumn := -1 // a header column named id: checked for being a usable key
	for j, h := range header {
		if snakeName(h) == "id" && idColumn < 0 {
			idColumn = j
		}
	}
	seen := map[string]bool{}
	idUnique := idColumn >= 0
	for {
		rec, err := src.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ImportPreview{}, readError(err)
		}
		preview.RowCount++
		for len(stats) < len(rec) {
			stats = append(stats, newColumnStats())
		}
		for j, v := range rec {
			stats[j].add(v)
		}
		if idUnique {
			v := ""
			if idColumn < len(rec) {
				v = rec[idColumn]
			}
			idUnique = v != "" && !seen[v]
			seen[v] = true
		}
		if len(preview.Rows) < importPreviewRows {
			preview.Rows = append(preview.Rows, append([]string(nil), rec...))
		}
	}
	width := max(len(header), len(stats))
	if width == 0 {
		return ImportPreview{}, errors.New("The file has no columns.")
	}
	for len(stats) < width {
		stats = append(stats, newColumnStats())
	}
	used := map[string]bool{}
	for j := range width {
		col := ImportColumn{Type: stats[j].pgType(), Empty: stats[j].empty}
		if j < len(header) {
			col.Header = header[j]
		}
		col.Name = uniqueName(snakeName(col.Header), fmt.Sprintf("column_%d", j+1), used)
		preview.Columns = append(preview.Columns, col)
	}
	for i := range preview.Rows { // pad short rows, so every row has every column
		for len(preview.Rows[i]) < width {
			preview.Rows[i] = append(preview.Rows[i], "")
		}
	}
	if idUnique && preview.RowCount > 0 && (stats[idColumn].pgType() == "bigint") {
		preview.KeyColumn = idColumn
	}
	base := strings.TrimSuffix(filepath.Base(opts.Path), filepath.Ext(opts.Path))
	preview.TableName = uniqueName(snakeName(base), "imported", map[string]bool{})
	return preview, nil
}

// ImportTargetColumns lists an existing table's columns for matching.
func (s *DBService) ImportTargetColumns(schema, table string) ([]ImportTargetColumn, error) {
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	if pool == nil {
		return nil, errors.New("Not connected.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	return importTargetColumns(ctx, pool, schema, table)
}

const importTargetQuery = `
SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod),
       a.attnotnull AND NOT a.atthasdef AND a.attidentity = '',
       a.attidentity = 'a' OR a.attgenerated <> ''
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

func importTargetColumns(ctx context.Context, q querier, schema, table string) ([]ImportTargetColumn, error) {
	rows, err := q.Query(ctx, importTargetQuery, schema, table)
	if err != nil {
		return nil, toQueryError(err)
	}
	columns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ImportTargetColumn, error) {
		var c ImportTargetColumn
		err := row.Scan(&c.Name, &c.Type, &c.Required, &c.Generated)
		return c, err
	})
	if err != nil {
		return nil, toQueryError(err)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("Table %s.%s was not found. Refresh the sidebar.", schema, table)
	}
	return columns, nil
}

// RunImport imports the file in one transaction on a pool connection (not
// the SQL editor's, so a transaction left open there can't swallow it),
// with the editor session's role and formats when it's free to ask.
func (s *DBService) RunImport(ctx context.Context, req ImportRequest) (ImportResult, error) {
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	if pool == nil {
		return ImportResult{}, errors.New("Not connected.")
	}
	targets, err := checkImport(req)
	if err != nil {
		return ImportResult{}, err
	}
	session := s.editorSession(ctx)
	started := time.Now()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return ImportResult{}, toQueryError(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) // no-op after Commit
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return ImportResult{}, toQueryError(err)
	}
	if err := applySession(ctx, tx, session); err != nil {
		return ImportResult{}, toQueryError(err)
	}
	table := pgx.Identifier{req.Schema, req.Table}.Sanitize()
	if req.NewTable {
		if _, err := tx.Exec(ctx, createTableSQL(table, req)); err != nil {
			return ImportResult{}, importError(err)
		}
	} else {
		columns, err := importTargetColumns(ctx, tx, req.Schema, req.Table)
		if err != nil {
			return ImportResult{}, err
		}
		byName := map[string]ImportTargetColumn{}
		for _, c := range columns {
			byName[c.Name] = c
		}
		for _, t := range targets {
			c, ok := byName[t]
			switch {
			case !ok:
				return ImportResult{}, fmt.Errorf("%s has no column %q. Nothing was imported.", table, t)
			case c.Generated:
				return ImportResult{}, fmt.Errorf("Column %q is generated by Postgres and can't be imported into. Skip it.", t)
			}
		}
	}

	quoted := make([]string, len(targets))
	for i, t := range targets {
		quoted[i] = pgx.Identifier{t}.Sanitize()
	}
	copySQL := fmt.Sprintf("COPY %s (%s) FROM STDIN WITH (FORMAT csv", table, strings.Join(quoted, ", "))
	if !req.EmptyAsNull {
		copySQL += ", FORCE_NOT_NULL (" + strings.Join(quoted, ", ") + ")"
	}
	copySQL += ")"

	// Stream the chosen columns of every row into COPY as CSV. Postgres
	// parses each value with the column's type, as if typed in SQL.
	reader, writer := io.Pipe()
	produced := make(chan error, 1)
	go func() {
		err := writeImportRows(writer, req)
		writer.CloseWithError(err)
		produced <- err
	}()
	tag, err := tx.Conn().PgConn().CopyFrom(ctx, reader, copySQL)
	// If COPY stopped first (a bad value, or cancelled), this ends the writer.
	reader.CloseWithError(errImportStopped)
	if readErr := <-produced; readErr != nil && !errors.Is(readErr, errImportStopped) {
		return ImportResult{}, readError(readErr) // the file itself couldn't be read
	}
	if err != nil {
		return ImportResult{}, importError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ImportResult{}, importError(err)
	}
	return ImportResult{Schema: req.Schema, Table: req.Table, Rows: int(tag.RowsAffected()), Created: req.NewTable,
		DurationMs: int(time.Since(started).Milliseconds())}, nil
}

// editorSession is the SQL editor session's role and formats, so an import
// reads dates and runs as the editor would; nil if a query is running there.
func (s *DBService) editorSession(ctx context.Context) map[string]string {
	if !s.queryMu.TryLock() {
		return nil
	}
	defer s.queryMu.Unlock()
	s.mu.Lock()
	editor := s.editor
	s.mu.Unlock()
	if editor == nil {
		return nil
	}
	return sessionOf(ctx, editor.Conn())
}

// checkImport validates a request and returns the target columns, in file
// column order.
func checkImport(req ImportRequest) ([]string, error) {
	if strings.TrimSpace(req.Schema) == "" || strings.TrimSpace(req.Table) == "" {
		return nil, errors.New("Choose the table to import into.")
	}
	var targets []string
	used := map[string]bool{}
	if req.NewTable && req.AddKey != "" {
		used[req.AddKey] = true
	}
	for i, c := range req.Choices {
		if c.Target == "" {
			continue
		}
		if used[c.Target] {
			return nil, fmt.Errorf("Two columns are both named %q.", c.Target)
		}
		used[c.Target] = true
		if req.NewTable && !importTypes[c.Type] {
			return nil, fmt.Errorf("Column %q has no type.", c.Target)
		}
		if req.KeyColumn == i && c.Target == "" {
			return nil, errors.New("The primary key column is skipped.")
		}
		targets = append(targets, c.Target)
	}
	if len(targets) == 0 {
		return nil, errors.New("Choose at least one column to import.")
	}
	if req.NewTable && req.KeyColumn >= 0 && (req.KeyColumn >= len(req.Choices) || req.Choices[req.KeyColumn].Target == "") {
		return nil, errors.New("The primary key column is skipped.")
	}
	return targets, nil
}

// createTableSQL is CREATE TABLE for a new table from the request.
func createTableSQL(table string, req ImportRequest) string {
	var defs []string
	if req.AddKey != "" {
		defs = append(defs, pgx.Identifier{req.AddKey}.Sanitize()+" bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY")
	}
	for _, c := range req.Choices {
		if c.Target != "" {
			defs = append(defs, pgx.Identifier{c.Target}.Sanitize()+" "+c.Type)
		}
	}
	if req.AddKey == "" && req.KeyColumn >= 0 {
		defs = append(defs, "PRIMARY KEY ("+pgx.Identifier{req.Choices[req.KeyColumn].Target}.Sanitize()+")")
	}
	return "CREATE TABLE " + table + " (" + strings.Join(defs, ", ") + ")"
}

// writeImportRows writes each data row's chosen columns to w as CSV.
func writeImportRows(w io.Writer, req ImportRequest) error {
	src, _, err := openImportRows(req.ImportOptions)
	if err != nil {
		return err
	}
	defer src.Close()
	if req.HasHeader {
		if _, err := src.Read(); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	out := csv.NewWriter(w)
	record := make([]string, 0, len(req.Choices))
	for row := 1; ; row++ {
		rec, err := src.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if len(rec) > len(req.Choices) && strings.Join(rec[len(req.Choices):], "") != "" {
			return fmt.Errorf("Row %d has %d values, but the file has %d columns. Nothing was imported.", row, len(rec), len(req.Choices))
		}
		record = record[:0]
		for j, c := range req.Choices {
			if c.Target == "" {
				continue
			}
			v := ""
			if j < len(rec) {
				v = rec[j]
			}
			record = append(record, v)
		}
		if err := out.Write(record); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

// errImportStopped ends the file reader when COPY has stopped.
var errImportStopped = errors.New("import stopped")

// copyLine reads where a COPY error happened: "COPY t, line 3, column x: …".
var copyLine = regexp.MustCompile(`line (\d+)(?:, column ([^:]+))?`)

// importError explains a failed import, pointing at the row when Postgres says.
func importError(err error) error {
	if errors.Is(err, context.Canceled) {
		return &QueryError{Message: "Import cancelled. Nothing was imported."}
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return toQueryError(err)
	}
	qe := toQueryError(err)
	qe.Position = 0
	switch {
	case pgErr.Code == "42P07": // duplicate_table
		qe.Message = "A table with that name already exists. Choose another name, or import into it instead."
		return qe
	case pgErr.Code == "57014":
		qe.Message = "Import cancelled. Nothing was imported."
		return qe
	}
	if m := copyLine.FindStringSubmatch(pgErr.Where); m != nil {
		row, _ := strconv.Atoi(m[1])
		where := fmt.Sprintf("Row %s", formatCount(row))
		if m[2] != "" {
			where += fmt.Sprintf(", column %s", m[2])
		}
		qe.Message = where + ": " + qe.Message
	}
	qe.Message += " Nothing was imported."
	return qe
}

// readError explains a file that couldn't be read.
func readError(err error) error {
	var parse *csv.ParseError
	if errors.As(err, &parse) {
		return fmt.Errorf("Line %d of the file can't be read as CSV: %v.", parse.Line, parse.Err)
	}
	return err
}

func formatCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// ---- reading files ----

// importRows reads a file's rows, one at a time; io.EOF at the end.
type importRows interface {
	Read() ([]string, error)
	Close() error
}

type importInfo struct {
	format    string
	sheets    []string
	sheet     string
	delimiter string
	notes     []string
}

func openImportRows(opts ImportOptions) (importRows, importInfo, error) {
	if strings.TrimSpace(opts.Path) == "" {
		return nil, importInfo{}, errors.New("Choose a file to import.")
	}
	switch strings.ToLower(filepath.Ext(opts.Path)) {
	case ".xlsx", ".xlsm":
		return openXLSX(opts)
	case ".xls":
		return nil, importInfo{}, errors.New("Old .xls files aren't supported. Save it as .xlsx or CSV from Excel.")
	default:
		return openCSV(opts)
	}
}

// csvRows reads a CSV file, decoded to UTF-8.
type csvRows struct {
	file *os.File
	r    *csv.Reader
}

func (c *csvRows) Read() ([]string, error) { return c.r.Read() }
func (c *csvRows) Close() error            { return c.file.Close() }

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func openCSV(opts ImportOptions) (importRows, importInfo, error) {
	f, err := os.Open(opts.Path)
	if err != nil {
		return nil, importInfo{}, fmt.Errorf("Can't open the file: %v", err)
	}
	info := importInfo{format: "csv"}
	br := bufio.NewReaderSize(f, 1<<20)
	head, _ := br.Peek(1 << 20)
	truncated := len(head) == 1<<20 // the sample may end inside a character
	if bytes.HasPrefix(head, utf8BOM) {
		br.Discard(len(utf8BOM))
		head = head[len(utf8BOM):]
	}
	var r io.Reader = br
	if !validUTF8Prefix(head, truncated) {
		r = &cp1252Reader{r: br}
		head = []byte(decodeCP1252(head))
		info.notes = append(info.notes, "The file isn't UTF-8, so it was read as Windows-1252 (Excel's usual CSV encoding).")
	}
	info.delimiter = opts.Delimiter
	if info.delimiter == "" {
		info.delimiter = detectDelimiter(string(head), opts.Path)
	}
	delim, _ := utf8.DecodeRuneInString(info.delimiter)
	if delim == utf8.RuneError || delim == '"' || delim == '\r' || delim == '\n' {
		f.Close()
		return nil, importInfo{}, errors.New("Choose another separator.")
	}
	cr := csv.NewReader(r)
	cr.Comma = delim
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	return &csvRows{file: f, r: cr}, info, nil
}

// validUTF8Prefix reports whether b is UTF-8. If b was cut from a longer
// file, a character cut off at the very end doesn't count against it.
func validUTF8Prefix(b []byte, truncated bool) bool {
	if truncated {
		for k := 1; k <= 3 && k <= len(b); k++ {
			if tail := b[len(b)-k:]; utf8.RuneStart(tail[0]) && !utf8.FullRune(tail) {
				b = b[:len(b)-k]
				break
			}
		}
	}
	return utf8.Valid(b)
}

// detectDelimiter picks the separator that splits the first lines into the
// same number of fields: comma, semicolon, tab or pipe.
func detectDelimiter(head, path string) string {
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".tsv" || ext == ".tab" {
		return "\t"
	}
	lines := strings.Split(head, "\n")
	if len(lines) > 1 {
		lines = lines[:len(lines)-1] // the last one may be cut off
	}
	best, bestCount := ",", 0
	for _, d := range []byte{',', ';', '\t', '|'} {
		count, consistent, n := -1, true, 0
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			c, quoted := 0, false
			for i := 0; i < len(line); i++ {
				switch {
				case line[i] == '"':
					quoted = !quoted
				case line[i] == d && !quoted:
					c++
				}
			}
			if count >= 0 && c != count {
				consistent = false
			}
			count = c
			if n++; n == 20 {
				break
			}
		}
		if consistent && count > bestCount {
			best, bestCount = string(d), count
		}
	}
	return best
}

// Windows-1252's 0x80–0x9F; the rest of its upper half is Latin-1.
var cp1252High = [32]rune{
	'€', 0xFFFD, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0xFFFD, 'Ž', 0xFFFD,
	0xFFFD, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0xFFFD, 'ž', 'Ÿ',
}

func cp1252Rune(b byte) rune {
	if b >= 0x80 && b < 0xA0 {
		return cp1252High[b-0x80]
	}
	return rune(b)
}

func decodeCP1252(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		out = utf8.AppendRune(out, cp1252Rune(c))
	}
	return string(out)
}

// cp1252Reader decodes Windows-1252 to UTF-8 as it reads.
type cp1252Reader struct {
	r   io.Reader
	out []byte
	err error
}

func (c *cp1252Reader) Read(p []byte) (int, error) {
	for len(c.out) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		buf := make([]byte, 32<<10)
		n, err := c.r.Read(buf)
		for _, b := range buf[:n] {
			c.out = utf8.AppendRune(c.out, cp1252Rune(b))
		}
		c.err = err
	}
	n := copy(p, c.out)
	c.out = c.out[n:]
	return n, nil
}

// xlsxRows reads a sheet of an Excel file: raw values (no thousands
// separators), with date and time cells written the way Postgres reads them.
type xlsxRows struct {
	f        *excelize.File
	sheet    string
	rows     [][]string
	next     int
	date1904 bool
	kinds    map[int]excelKind // per style
}

type excelKind int

const (
	excelPlain excelKind = iota
	excelDate
	excelDateTime
	excelTime
)

func openXLSX(opts ImportOptions) (importRows, importInfo, error) {
	f, err := excelize.OpenFile(opts.Path)
	if err != nil {
		return nil, importInfo{}, fmt.Errorf("Can't read the Excel file: %v", err)
	}
	info := importInfo{format: "xlsx", sheets: f.GetSheetList()}
	if len(info.sheets) == 0 {
		f.Close()
		return nil, importInfo{}, errors.New("The Excel file has no sheets.")
	}
	info.sheet = opts.Sheet
	if info.sheet == "" {
		info.sheet = info.sheets[0]
	} else if idx, _ := f.GetSheetIndex(info.sheet); idx < 0 {
		f.Close()
		return nil, importInfo{}, fmt.Errorf("The file has no sheet %q.", info.sheet)
	}
	rows, err := f.GetRows(info.sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		f.Close()
		return nil, importInfo{}, fmt.Errorf("Can't read sheet %q: %v", info.sheet, err)
	}
	x := &xlsxRows{f: f, sheet: info.sheet, rows: rows, kinds: map[int]excelKind{}}
	if props, err := f.GetWorkbookProps(); err == nil && props.Date1904 != nil {
		x.date1904 = *props.Date1904
	}
	return x, info, nil
}

func (x *xlsxRows) Close() error { return x.f.Close() }

func (x *xlsxRows) Read() ([]string, error) {
	if x.next >= len(x.rows) {
		return nil, io.EOF
	}
	row := x.rows[x.next]
	x.next++
	out := make([]string, len(row))
	for j, v := range row {
		out[j] = x.cell(x.next, j+1, v)
	}
	return out, nil
}

// cell converts one raw value: TRUE/FALSE for boolean cells, ISO dates and
// times for numbers shown with a date or time format.
func (x *xlsxRows) cell(row, col int, raw string) string {
	if raw == "" {
		return raw
	}
	serial, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw // text, or an error value like #DIV/0!
	}
	name, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		return raw
	}
	if raw == "0" || raw == "1" {
		if t, err := x.f.GetCellType(x.sheet, name); err == nil && t == excelize.CellTypeBool {
			return map[string]string{"0": "false", "1": "true"}[raw]
		}
	}
	style, err := x.f.GetCellStyle(x.sheet, name)
	if err != nil {
		return raw
	}
	kind, ok := x.kinds[style]
	if !ok {
		kind = excelPlain
		if st, err := x.f.GetStyle(style); err == nil {
			custom := ""
			if st.CustomNumFmt != nil {
				custom = *st.CustomNumFmt
			}
			kind = excelFormatKind(st.NumFmt, custom)
		}
		x.kinds[style] = kind
	}
	if kind == excelPlain {
		return raw
	}
	t, err := excelize.ExcelDateToTime(serial, x.date1904)
	if err != nil {
		return raw
	}
	whole := serial == float64(int64(serial))
	switch {
	case kind == excelTime:
		return t.Format("15:04:05")
	case kind == excelDate && whole:
		return t.Format("2006-01-02")
	case t.Nanosecond() != 0:
		return t.Format("2006-01-02 15:04:05.000")
	default:
		return t.Format("2006-01-02 15:04:05")
	}
}

// excelFormatKind tells date, date-time and time number formats from the rest.
func excelFormatKind(id int, custom string) excelKind {
	switch {
	case id == 22:
		return excelDateTime
	case id >= 14 && id <= 17, id >= 27 && id <= 31, id >= 34 && id <= 36, id >= 50 && id <= 58:
		return excelDate
	case id >= 18 && id <= 21, id == 32, id == 33, id >= 45 && id <= 47:
		return excelTime
	case custom == "":
		return excelPlain
	}
	// Drop quoted text, escaped characters and [colour]/[$-locale] parts.
	var b strings.Builder
	quoted := false
	for i := 0; i < len(custom); i++ {
		c := custom[i]
		switch {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '\\' || c == '_' || c == '*':
			i++
		case c == '[':
			end := strings.IndexByte(custom[i:], ']')
			if end < 0 {
				i = len(custom)
				continue
			}
			inner := strings.ToLower(custom[i+1 : i+end])
			if strings.Trim(inner, "hms") == "" { // [h], [mm], [ss]: elapsed time
				b.WriteString(inner)
			}
			i += end
		default:
			b.WriteByte(byte(unicode.ToLower(rune(c))))
		}
	}
	f := b.String()
	date := strings.ContainsAny(f, "yd")
	clock := strings.ContainsAny(f, "hs")
	switch {
	case date && clock:
		return excelDateTime
	case date:
		return excelDate
	case clock:
		return excelTime
	}
	return excelPlain
}

// ---- suggestions for a new table ----

var (
	intValue  = regexp.MustCompile(`^[-+]?(0|[1-9][0-9]*)$`)
	numValue  = regexp.MustCompile(`^[-+]?((0|[1-9][0-9]*)(\.[0-9]*)?|\.[0-9]+)([eE][-+]?[0-9]+)?$`) // no leading zeros: 007 is a code
	boolValue = regexp.MustCompile(`(?i)^(true|false|t|f|yes|no)$`)
	dateValue = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	tsValue   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9]{2}:[0-9]{2}(:[0-9]{2}(\.[0-9]+)?)?(Z|[-+][0-9]{2}(:?[0-9]{2})?)?$`)
	tzSuffix  = regexp.MustCompile(`(Z|[-+][0-9]{2}(:?[0-9]{2})?)$`)
)

// columnStats watches every value of a column to suggest its type.
type columnStats struct {
	empty, filled                      int
	isInt, isNum, isBool, isDate, isTS bool
	withZone                           bool
}

func newColumnStats() columnStats {
	return columnStats{isInt: true, isNum: true, isBool: true, isDate: true, isTS: true}
}

func (c *columnStats) add(v string) {
	if v == "" {
		c.empty++
		return
	}
	c.filled++
	if c.isInt {
		_, err := strconv.ParseInt(v, 10, 64)
		c.isInt = err == nil && intValue.MatchString(v)
	}
	c.isNum = c.isNum && numValue.MatchString(v)
	c.isBool = c.isBool && boolValue.MatchString(v)
	c.isDate = c.isDate && dateValue.MatchString(v)
	if c.isTS {
		c.isTS = tsValue.MatchString(v)
		c.withZone = c.withZone || c.isTS && tzSuffix.MatchString(v[10:])
	}
}

// pgType suggests a type every value fits; text when unsure or all empty.
func (c columnStats) pgType() string {
	switch {
	case c.filled == 0:
		return "text"
	case c.isInt:
		return "bigint"
	case c.isNum:
		return "numeric"
	case c.isBool:
		return "boolean"
	case c.isDate:
		return "date"
	case c.isTS && c.withZone:
		return "timestamptz"
	case c.isTS:
		return "timestamp"
	}
	return "text"
}

// snakeName turns a header into a column name: lower case, words joined by _.
func snakeName(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) { // marks: vowel signs, accents
			if underscore && b.Len() > 0 {
				b.WriteByte('_')
			}
			underscore = false
			b.WriteRune(unicode.ToLower(r))
		} else {
			underscore = true
		}
	}
	return b.String()
}

// uniqueName is name (or fallback when empty), with _2, _3… if already used.
func uniqueName(name, fallback string, used map[string]bool) string {
	if name == "" {
		name = fallback
	}
	if len(name) > 60 {
		name = name[:60]
	}
	candidate := name
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s_%d", name, i)
	}
	used[candidate] = true
	return candidate
}
