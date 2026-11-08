package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestDetectDelimiter(t *testing.T) {
	cases := []struct{ head, path, want string }{
		{"a,b,c\n1,2,3\n", "x.csv", ","},
		{"a;b;c\n1;2,5;3\n", "x.csv", ";"},
		{"a\tb\n1\t2\n", "x.txt", "\t"},
		{"a,b\n1,2\n", "x.tsv", "\t"},
		{"a|b|c\n1|2|3\n", "x.csv", "|"},
		{"name,city\n\"Smith, John\",Pune\n", "x.csv", ","},
		{"single\nvalue\n", "x.csv", ","},
	}
	for _, c := range cases {
		if got := detectDelimiter(c.head, c.path); got != c.want {
			t.Errorf("%q: got %q, want %q", c.head, got, c.want)
		}
	}
}

func TestExcelFormatKind(t *testing.T) {
	cases := []struct {
		id     int
		custom string
		want   excelKind
	}{
		{14, "", excelDate}, {22, "", excelDateTime}, {18, "", excelTime}, {4, "", excelPlain}, {0, "", excelPlain},
		{0, "dd/mm/yyyy", excelDate}, {0, "hh:mm", excelTime}, {0, "yyyy-mm-dd hh:mm:ss", excelDateTime},
		{0, "#,##0.00", excelPlain}, {0, `0.00" days"`, excelPlain}, {0, "[$-409]d-mmm-yy", excelDate},
		{0, "[h]:mm:ss", excelTime}, {0, "General", excelPlain}, {0, "[Red]#,##0", excelPlain},
	}
	for _, c := range cases {
		if got := excelFormatKind(c.id, c.custom); got != c.want {
			t.Errorf("%d %q: got %d, want %d", c.id, c.custom, got, c.want)
		}
	}
}

func TestColumnTypes(t *testing.T) {
	cases := []struct {
		values []string
		want   string
	}{
		{[]string{"1", "-20", "", "300"}, "bigint"},
		{[]string{"007", "12"}, "text"}, // leading zeros: codes, not numbers
		{[]string{"1.5", "2", "-3.25", "1e5"}, "numeric"},
		{[]string{"99999999999999999999"}, "numeric"}, // too big for bigint
		{[]string{"true", "False", "yes", "NO", "t"}, "boolean"},
		{[]string{"1", "0", "1"}, "bigint"},
		{[]string{"2026-10-05", "2025-01-31"}, "date"},
		{[]string{"2026-10-05 14:30:00", "2026-10-05T09:00"}, "timestamp"},
		{[]string{"2026-10-05 14:30:00+05:30", "2026-10-05 09:00:00"}, "timestamptz"},
		{[]string{"2026-10-05", "soon"}, "text"},
		{[]string{"", ""}, "text"},
	}
	for _, c := range cases {
		stats := newColumnStats()
		for _, v := range c.values {
			stats.add(v)
		}
		if got := stats.pgType(); got != c.want {
			t.Errorf("%q: got %s, want %s", c.values, got, c.want)
		}
	}
}

func TestSnakeAndUniqueNames(t *testing.T) {
	for in, want := range map[string]string{
		"First Name": "first_name", "  Amount (₹) ": "amount", "ID": "id", "2024 Sales": "2024_sales",
		"नाम": "नाम", "already_snake": "already_snake", "---": "",
	} {
		if got := snakeName(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	used := map[string]bool{}
	got := []string{uniqueName("a", "x", used), uniqueName("a", "x", used), uniqueName("", "column_3", used)}
	if !slices.Equal(got, []string{"a", "a_2", "column_3"}) {
		t.Errorf("unique names %v", got)
	}
}

func TestDecodeCP1252(t *testing.T) {
	if got := decodeCP1252([]byte("caf\xe9 \x80 \x93q\x94")); got != "café € “q”" {
		t.Errorf("got %q", got)
	}
}

// writeFile writes a file in the test's temp dir and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// choices maps file columns to targets ("" skips) with the given types.
func choices(targets []string, types ...string) []ImportChoice {
	out := make([]ImportChoice, len(targets))
	for i, target := range targets {
		out[i].Target = target
		if i < len(types) {
			out[i].Type = types[i]
		}
	}
	return out
}

func TestImportCSVIntoExistingTable(t *testing.T) {
	s := editFixture(t, "CREATE TABLE "+testSchema+".acct (id int PRIMARY KEY, owner text, balance numeric, joined date)")
	// Columns in another order and case, a quoted comma, an empty value, a skipped column.
	path := writeFile(t, "accounts.csv", "Owner,Balance,ID,Joined,Notes\n"+
		"\"Smith, John\",100.5,1,2026-10-05,vip\n"+
		"alice,,2,2026-01-31,\n")
	opts := ImportOptions{Path: path, HasHeader: true}
	preview, err := s.PreviewImport(opts)
	if err != nil {
		t.Fatal(err)
	}
	if preview.RowCount != 2 || preview.Delimiter != "," || len(preview.Columns) != 5 || preview.Rows[0][0] != "Smith, John" {
		t.Fatalf("preview %+v", preview)
	}
	result, err := s.RunImport(t.Context(), ImportRequest{ImportOptions: opts, EmptyAsNull: true, Schema: testSchema, Table: "acct",
		Choices: choices([]string{"owner", "balance", "id", "joined", ""}), KeyColumn: -1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 2 || result.Created {
		t.Fatalf("result %+v", result)
	}
	got := table(t, s, "SELECT id, owner, coalesce(balance::text, 'NULL'), joined::text FROM "+testSchema+".acct ORDER BY id")
	if !slices.Equal(got, []string{"1|Smith, John|100.5|2026-10-05", "2|alice|NULL|2026-01-31"}) {
		t.Fatalf("table %v", got)
	}
}

func TestImportNewTableFromCSV(t *testing.T) {
	s := editFixture(t)
	// A semicolon file with a byte-order mark, as Excel writes in many locales.
	path := writeFile(t, "Customer List.csv", "\xEF\xBB\xBFID;Full Name;Balance;Active;Joined;Seen At;Code\n"+
		"1;Asha Rao;100,5;yes;2026-10-05;2026-10-05 09:30:00+05:30;007\n"+
		"2;Ben;20;no;2026-01-31;2026-01-31 18:00:00Z;012\n")
	preview, err := s.PreviewImport(ImportOptions{Path: path, HasHeader: true})
	if err != nil {
		t.Fatal(err)
	}
	var names, types []string
	for _, c := range preview.Columns {
		names, types = append(names, c.Name), append(types, c.Type)
	}
	if preview.Delimiter != ";" || preview.TableName != "customer_list" || preview.KeyColumn != 0 ||
		!slices.Equal(names, []string{"id", "full_name", "balance", "active", "joined", "seen_at", "code"}) ||
		!slices.Equal(types, []string{"bigint", "text", "text", "boolean", "date", "timestamptz", "text"}) {
		t.Fatalf("preview %v %v key %d table %s delim %q", names, types, preview.KeyColumn, preview.TableName, preview.Delimiter)
	}
	req := ImportRequest{ImportOptions: ImportOptions{Path: path, HasHeader: true}, EmptyAsNull: true, Schema: testSchema,
		Table: preview.TableName, NewTable: true, Choices: choices(names, types...), KeyColumn: preview.KeyColumn}
	result, err := s.RunImport(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 2 || !result.Created {
		t.Fatalf("result %+v", result)
	}
	if got := table(t, s, "SELECT id, full_name, active, code FROM "+testSchema+".customer_list ORDER BY id"); !slices.Equal(got,
		[]string{"1|Asha Rao|true|007", "2|Ben|false|012"}) {
		t.Fatalf("table %v", got)
	}
	// The new table has its primary key, so SpyDB can edit it right away.
	page, err := s.TableData(t.Context(), TableQuery{Schema: testSchema, Table: "customer_list", PageSize: 100})
	if err != nil || page.Target == nil || !slices.Equal(page.Target.Key, []string{"id"}) {
		t.Fatalf("not editable: %v %+v", err, page.Target)
	}
	// Importing again under the same name: refused, nothing changes.
	if _, err := s.RunImport(t.Context(), req); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("same name again: %v", err)
	}
}

func TestImportNewTableAddsKey(t *testing.T) {
	s := editFixture(t)
	path := writeFile(t, "names.csv", "name\nasha\nben\n")
	result, err := s.RunImport(t.Context(), ImportRequest{ImportOptions: ImportOptions{Path: path, HasHeader: true}, EmptyAsNull: true,
		Schema: testSchema, Table: "names", NewTable: true, Choices: choices([]string{"name"}, "text"), AddKey: "id", KeyColumn: -1})
	if err != nil || result.Rows != 2 {
		t.Fatalf("%v %+v", err, result)
	}
	if got := table(t, s, "SELECT id, name FROM "+testSchema+".names ORDER BY id"); !slices.Equal(got, []string{"1|asha", "2|ben"}) {
		t.Fatalf("table %v", got)
	}
}

func TestImportBadRowChangesNothing(t *testing.T) {
	s := editFixture(t, "CREATE TABLE "+testSchema+".acct (id int PRIMARY KEY, balance numeric)")
	path := writeFile(t, "bad.csv", "id,balance\n1,10\n2,20\n3,abc\n4,40\n")
	opts := ImportOptions{Path: path, HasHeader: true}
	_, err := s.RunImport(t.Context(), ImportRequest{ImportOptions: opts, EmptyAsNull: true, Schema: testSchema, Table: "acct",
		Choices: choices([]string{"id", "balance"}), KeyColumn: -1})
	if err == nil || !strings.Contains(err.Error(), "Row 3, column balance") || !strings.Contains(err.Error(), "Nothing was imported") {
		t.Fatalf("got %v", err)
	}
	if got := table(t, s, "SELECT count(*) FROM "+testSchema+".acct"); got[0] != "0" {
		t.Fatalf("rows left behind: %v", got)
	}
	// A new table whose import fails isn't created either.
	_, err = s.RunImport(t.Context(), ImportRequest{ImportOptions: opts, EmptyAsNull: true, Schema: testSchema, Table: "fresh",
		NewTable: true, Choices: choices([]string{"id", "balance"}, "bigint", "numeric"), KeyColumn: -1})
	if err == nil {
		t.Fatal("bad row imported")
	}
	if got := table(t, s, "SELECT coalesce(to_regclass('"+testSchema+".fresh')::text, 'none')"); got[0] != "none" {
		t.Fatalf("table created anyway: %v", got)
	}
	// A duplicate key is a bad row too.
	path = writeFile(t, "dupe.csv", "id,balance\n1,10\n1,20\n")
	_, err = s.RunImport(t.Context(), ImportRequest{ImportOptions: ImportOptions{Path: path, HasHeader: true}, EmptyAsNull: true,
		Schema: testSchema, Table: "acct", Choices: choices([]string{"id", "balance"}), KeyColumn: -1})
	if err == nil || !strings.Contains(err.Error(), "uplicate key") {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestImportExcel(t *testing.T) {
	s := editFixture(t)
	f := excelize.NewFile()
	sheet := "Sheet1"
	f.SetSheetRow(sheet, "A1", &[]any{"Name", "Joined", "At", "Amount", "Active", "Code"})
	dmy := "dd/mm/yyyy"
	dateStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: &dmy})
	stampStyle, _ := f.NewStyle(&excelize.Style{NumFmt: 22})
	moneyStyle, _ := f.NewStyle(&excelize.Style{NumFmt: 4})
	f.SetSheetRow(sheet, "A2", &[]any{"asha", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 5, 14, 30, 15, 0, time.UTC), 1234567.5, true, "007"})
	f.SetCellStyle(sheet, "B2", "B2", dateStyle)
	f.SetCellStyle(sheet, "C2", "C2", stampStyle)
	f.SetCellStyle(sheet, "D2", "D2", moneyStyle)
	f.NewSheet("Other")
	f.SetSheetRow("Other", "A1", &[]any{"x"})
	f.SetSheetRow("Other", "A2", &[]any{42})
	path := filepath.Join(t.TempDir(), "people.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}

	opts := ImportOptions{Path: path, HasHeader: true}
	preview, err := s.PreviewImport(opts)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, c := range preview.Columns {
		types = append(types, c.Type)
	}
	// 5 October, not May 10; no thousands separators; TRUE as true; 007 kept.
	if preview.Format != "xlsx" || !slices.Equal(preview.Sheets, []string{"Sheet1", "Other"}) ||
		!slices.Equal(preview.Rows[0], []string{"asha", "2026-10-05", "2026-10-05 14:30:15", "1234567.5", "true", "007"}) ||
		!slices.Equal(types, []string{"text", "date", "timestamp", "numeric", "boolean", "text"}) {
		t.Fatalf("preview %v %v %v", preview.Sheets, preview.Rows, types)
	}
	names := []string{"name", "joined", "at", "amount", "active", "code"}
	if _, err := s.RunImport(t.Context(), ImportRequest{ImportOptions: opts, EmptyAsNull: true, Schema: testSchema, Table: "people",
		NewTable: true, Choices: choices(names, types...), AddKey: "id", KeyColumn: -1}); err != nil {
		t.Fatal(err)
	}
	got := table(t, s, "SELECT joined::text, at::text, amount::text, active, code FROM "+testSchema+".people")
	if !slices.Equal(got, []string{"2026-10-05|2026-10-05 14:30:15|1234567.5|true|007"}) {
		t.Fatalf("table %v", got)
	}
	other, err := s.PreviewImport(ImportOptions{Path: path, HasHeader: true, Sheet: "Other"})
	if err != nil || other.Sheet != "Other" || other.Columns[0].Type != "bigint" || other.Rows[0][0] != "42" {
		t.Fatalf("other sheet: %v %+v", err, other)
	}
}

func TestImportEmptyCells(t *testing.T) {
	s := editFixture(t, "CREATE TABLE "+testSchema+".notes (id int PRIMARY KEY, note text NOT NULL)")
	path := writeFile(t, "notes.csv", "id,note\n1,\n")
	req := ImportRequest{ImportOptions: ImportOptions{Path: path, HasHeader: true}, EmptyAsNull: true, Schema: testSchema, Table: "notes",
		Choices: choices([]string{"id", "note"}), KeyColumn: -1}
	if _, err := s.RunImport(t.Context(), req); err == nil || !strings.Contains(err.Error(), "Row 1") {
		t.Fatalf("empty as NULL into NOT NULL: %v", err)
	}
	req.EmptyAsNull = false
	if _, err := s.RunImport(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if got := table(t, s, "SELECT id, length(note) FROM "+testSchema+".notes"); got[0] != "1|0" {
		t.Fatalf("table %v", got)
	}
}

func TestImportRefusals(t *testing.T) {
	s := editFixture(t, "CREATE TABLE "+testSchema+".gen (id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name text, upper_name text GENERATED ALWAYS AS (upper(name)) STORED, n int NOT NULL)")
	columns, err := s.ImportTargetColumns(testSchema, "gen")
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string][2]bool{}
	for _, c := range columns {
		flags[c.Name] = [2]bool{c.Required, c.Generated}
	}
	if flags["id"] != [2]bool{false, true} || flags["upper_name"] != [2]bool{false, true} || flags["n"] != [2]bool{true, false} || flags["name"] != [2]bool{} {
		t.Fatalf("columns %+v", columns)
	}
	path := writeFile(t, "gen.csv", "id,name\n1,a\n")
	if _, err := s.RunImport(t.Context(), ImportRequest{ImportOptions: ImportOptions{Path: path, HasHeader: true}, EmptyAsNull: true,
		Schema: testSchema, Table: "gen", Choices: choices([]string{"id", "name"}), KeyColumn: -1}); err == nil || !strings.Contains(err.Error(), "generated") {
		t.Fatalf("into a generated column: %v", err)
	}
	// A row wider than the file's columns.
	path = writeFile(t, "wide.csv", "name,n\na,1\nb,2,extra\n")
	if _, err := s.RunImport(t.Context(), ImportRequest{ImportOptions: ImportOptions{Path: path, HasHeader: true}, EmptyAsNull: true,
		Schema: testSchema, Table: "gen", Choices: choices([]string{"name", "n"}), KeyColumn: -1}); err == nil || !strings.Contains(err.Error(), "Row 2 has 3 values") {
		t.Fatalf("wide row: %v", err)
	}
	if got := table(t, s, "SELECT count(*) FROM "+testSchema+".gen"); got[0] != "0" {
		t.Fatalf("rows left behind: %v", got)
	}
}

func TestImportWindows1252(t *testing.T) {
	s := editFixture(t, "CREATE TABLE "+testSchema+".cafe (name text)")
	path := writeFile(t, "cafe.csv", "name\ncaf\xe9\n")
	preview, err := s.PreviewImport(ImportOptions{Path: path, HasHeader: true})
	if err != nil || preview.Rows[0][0] != "café" || len(preview.Notes) != 1 {
		t.Fatalf("%v %+v", err, preview)
	}
	if _, err := s.RunImport(t.Context(), ImportRequest{ImportOptions: ImportOptions{Path: path, HasHeader: true}, EmptyAsNull: true,
		Schema: testSchema, Table: "cafe", Choices: choices([]string{"name"}), KeyColumn: -1}); err != nil {
		t.Fatal(err)
	}
	if got := table(t, s, "SELECT name FROM "+testSchema+".cafe"); got[0] != "café" {
		t.Fatalf("table %v", got)
	}
}
