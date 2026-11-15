package mongodb

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestExportCollection(t *testing.T) {
	s, _ := testServer(t)
	ctx := context.Background()
	dir := t.TempDir()
	q := ExportQuery{Collection: "orders", Filters: []Filter{{Field: "status", Operator: "eq", Value: "PAID"}}, SortBy: "total", SortDesc: true}

	csvPath := dir + "/paid.csv"
	n, err := s.ExportCollection(ctx, q, csvPath, "csv")
	if err != nil || n != 40 {
		t.Fatalf("csv: %d %v", n, err)
	}
	data, _ := os.ReadFile(csvPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 41 || lines[0] != "_id,status,total,placed,address,note" ||
		lines[1] != `120,PAID,12000,2024-04-30T00:00:00.000Z,"{""city"":""Delhi"",""pin"":110001}",gift` ||
		lines[2] != `117,PAID,11700,2024-04-27T00:00:00.000Z,"{""city"":""Delhi"",""pin"":110001}",` {
		t.Fatalf("csv lines: %d\n%s\n%s\n%s", len(lines), lines[0], lines[1], lines[2])
	}

	xlsxPath := dir + "/all.xlsx"
	if n, err = s.ExportCollection(ctx, ExportQuery{Collection: "orders"}, xlsxPath, "xlsx"); err != nil || n != 120 {
		t.Fatalf("xlsx: %d %v", n, err)
	}
	book, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, _ := book.GetRows("Sheet1")
	if len(rows) != 121 || strings.Join(rows[0], ",") != "_id,status,total,placed,address,note" || rows[1][2] != "100" {
		t.Fatalf("xlsx rows: %d %v %v", len(rows), rows[0], rows[1])
	}
	if typ, _ := book.GetCellType("Sheet1", "C2"); typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset {
		t.Errorf("total should be a number in Excel, is %v", typ)
	}
	if _, err := s.ExportCollection(ctx, q, dir+"/x.pdf", "pdf"); err == nil {
		t.Error("an unknown format should be refused")
	}
}
