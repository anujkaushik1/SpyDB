package mongodb

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ExportQuery is what a collection tab exports: every document that matches
// the filters (all of them with none), in the tab's order.
type ExportQuery struct {
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters"`
	SortBy     string   `json:"sortBy"`
	SortDesc   bool     `json:"sortDesc"`
}

// Excel's limits: rows per sheet and characters per cell.
const (
	excelMaxRows      = 1_048_576
	excelMaxCellChars = 32_767
)

// ExportCollection writes the documents to path as CSV or Excel ("csv" or
// "xlsx"), one row each, with a column per field (_id first, then in the
// order fields first appear) and values as the grid shows them; objects
// and arrays as JSON. Documents needn't share fields, so they're read once
// into a temporary file while the columns are gathered, then written out:
// the server is read only once. It returns how many were written.
func (s *Service) ExportCollection(ctx context.Context, q ExportQuery, path, format string) (int, error) {
	if format != "csv" && format != "xlsx" {
		return 0, fmt.Errorf("Unknown export format %q.", format)
	}
	coll, _, err := s.collection(q.Collection)
	if err != nil {
		return 0, err
	}
	filter, err := filterDocument(q.Filters)
	if err != nil {
		return 0, err
	}
	opts := options.Find()
	if q.SortBy != "" {
		dir := 1
		if q.SortDesc {
			dir = -1
		}
		opts.SetSort(bson.D{{Key: q.SortBy, Value: dir}, {Key: "_id", Value: 1}})
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	spool, err := os.CreateTemp("", "spydb-export-*.jsonl")
	if err != nil {
		return 0, err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	out := bufio.NewWriter(spool)
	enc := json.NewEncoder(out)
	var names []string
	seen := map[string]bool{}
	types := map[string]map[string]bool{} // field → the types it holds, for Excel's numbers and booleans
	n := 0
	for cursor.Next(ctx) {
		doc, err := toDocument(cursor.Current)
		if err != nil {
			return 0, err
		}
		for _, name := range doc.Order {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
				types[name] = map[string]bool{}
			}
			if t := doc.Fields[name].Type; t != "null" {
				types[name][t] = true
			}
		}
		if err := enc.Encode(doc.Fields); err != nil {
			return 0, err
		}
		n++
		if format == "xlsx" && n+1 > excelMaxRows {
			return 0, fmt.Errorf("Excel sheets hold at most %d rows. Export as CSV instead.", excelMaxRows)
		}
	}
	if err := cursor.Err(); err != nil {
		return 0, err
	}
	if err := out.Flush(); err != nil {
		return 0, err
	}
	if i := slices.Index(names, "_id"); i > 0 {
		names = append([]string{"_id"}, slices.Delete(names, i, i+1)...)
	}

	if _, err := spool.Seek(0, 0); err != nil {
		return 0, err
	}
	w, err := newSheetWriter(path, format, names, types)
	if err != nil {
		return 0, err
	}
	lines := bufio.NewScanner(spool)
	lines.Buffer(make([]byte, 1<<20), 64<<20) // a document is at most 16 MB of BSON
	for lines.Scan() {
		var fields map[string]Value
		if err := json.Unmarshal(lines.Bytes(), &fields); err != nil {
			return 0, w.abort(err)
		}
		row := make([]*Value, len(names))
		for i, name := range names {
			if v, ok := fields[name]; ok && v.Type != "null" {
				row[i] = &v
			}
		}
		if err := w.write(row); err != nil {
			return 0, w.abort(err)
		}
	}
	if err := lines.Err(); err != nil {
		return 0, w.abort(err)
	}
	return n, w.close()
}

// sheetWriter writes rows to a CSV or Excel file; abort removes the partial file.
type sheetWriter struct {
	path   string
	file   *os.File      // CSV
	buf    *bufio.Writer // CSV
	csv    *csv.Writer
	book   *excelize.File // Excel
	stream *excelize.StreamWriter
	kinds  []string // per column: "number", "bool" or "" (text), for Excel
	rows   int
}

func newSheetWriter(path, format string, names []string, types map[string]map[string]bool) (*sheetWriter, error) {
	w := &sheetWriter{path: path, kinds: make([]string, len(names))}
	for i, name := range names {
		switch t := types[name]; {
		case len(t) > 0 && everyOf(t, "int", "long", "double", "decimal"):
			w.kinds[i] = "number"
		case len(t) == 1 && t["bool"]:
			w.kinds[i] = "bool"
		}
	}
	if format == "csv" {
		file, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		w.file, w.buf = file, bufio.NewWriter(file)
		w.csv = csv.NewWriter(w.buf)
		if err := w.csv.Write(names); err != nil {
			return nil, w.abort(err)
		}
		return w, nil
	}
	w.book = excelize.NewFile()
	stream, err := w.book.NewStreamWriter("Sheet1")
	if err != nil {
		return nil, w.abort(err)
	}
	w.stream = stream
	bold, err := w.book.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return nil, w.abort(err)
	}
	if err := stream.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return nil, w.abort(err)
	}
	if len(names) > 0 {
		if err := stream.SetColWidth(1, len(names), 18); err != nil {
			return nil, w.abort(err)
		}
	}
	header := make([]any, len(names))
	for i, name := range names {
		header[i] = excelize.Cell{StyleID: bold, Value: name}
	}
	return w, stream.SetRow("A1", header)
}

func everyOf(set map[string]bool, allowed ...string) bool {
	for t := range set {
		if !slices.Contains(allowed, t) {
			return false
		}
	}
	return true
}

// write adds one row; nil is an empty cell (null, or a field the document hasn't got).
func (w *sheetWriter) write(row []*Value) error {
	w.rows++
	if w.csv != nil {
		record := make([]string, len(row))
		for i, v := range row {
			if v != nil {
				record[i] = v.Text
			}
		}
		return w.csv.Write(record)
	}
	values := make([]any, len(row))
	for i, v := range row {
		if v != nil {
			values[i] = excelValue(v.Text, w.kinds[i])
		}
	}
	cell, err := excelize.CoordinatesToCellName(1, w.rows+1)
	if err != nil {
		return err
	}
	return w.stream.SetRow(cell, values)
}

func (w *sheetWriter) close() error {
	if w.csv != nil {
		w.csv.Flush()
		if err := w.csv.Error(); err != nil {
			return w.abort(err)
		}
		if err := w.buf.Flush(); err != nil {
			return w.abort(err)
		}
		if err := w.file.Close(); err != nil {
			os.Remove(w.path)
			return err
		}
		return nil
	}
	if err := w.stream.Flush(); err != nil {
		return w.abort(err)
	}
	if err := w.book.SaveAs(w.path); err != nil {
		return w.abort(err)
	}
	return w.book.Close()
}

func (w *sheetWriter) abort(err error) error {
	if w.file != nil {
		w.file.Close()
	}
	if w.book != nil {
		w.book.Close()
	}
	os.Remove(w.path)
	return err
}

// excelValue is what Excel stores: numbers as numbers when a float holds
// them exactly (at most 15 significant digits; longer ones, like big ids,
// stay text so no digit is lost), true / false as booleans, else text.
func excelValue(v, kind string) any {
	switch kind {
	case "number":
		digits := strings.ReplaceAll(strings.TrimLeft(strings.TrimPrefix(v, "-"), "0."), ".", "")
		if f, err := strconv.ParseFloat(v, 64); err == nil && len(digits) <= 15 && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	case "bool":
		return v == "true"
	}
	if len(v) > excelMaxCellChars {
		return v[:excelMaxCellChars]
	}
	return v
}
