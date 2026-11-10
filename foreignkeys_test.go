package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Every foreign key of a table once, in name order: single and composite
// columns, quoted names, a key to itself, and a key to a partitioned table
// (whose per-partition copies Postgres keeps in pg_constraint too).
func TestForeignKeys(t *testing.T) {
	s := editFixture(t,
		`CREATE TABLE `+testSchema+`.mandates (id text PRIMARY KEY)`,
		`CREATE TABLE `+testSchema+`."Branch" ("Code" int, region text, PRIMARY KEY ("Code", region))`,
		`CREATE TABLE `+testSchema+`.events (id int, at date, PRIMARY KEY (id, at)) PARTITION BY RANGE (at)`,
		`CREATE TABLE `+testSchema+`.events_2026 PARTITION OF `+testSchema+`.events FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')`,
		`CREATE TABLE `+testSchema+`.events_2027 PARTITION OF `+testSchema+`.events FOR VALUES FROM ('2027-01-01') TO ('2028-01-01')`,
		`CREATE TABLE `+testSchema+`.orders (
			id int PRIMARY KEY,
			mandate_id text CONSTRAINT a_mandate REFERENCES `+testSchema+`.mandates,
			branch_code int, branch_region text,
			parent_id int CONSTRAINT c_parent REFERENCES `+testSchema+`.orders (id),
			event_id int, event_at date,
			CONSTRAINT b_branch FOREIGN KEY (branch_region, branch_code) REFERENCES `+testSchema+`."Branch" (region, "Code"),
			CONSTRAINT d_event FOREIGN KEY (event_id, event_at) REFERENCES `+testSchema+`.events (id, at))`,
	)
	keys, err := s.ForeignKeys(t.Context(), testSchema, "orders")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range keys {
		got = append(got, fmt.Sprintf("%s: %s -> %s.%s %s", k.Name, strings.Join(k.Columns, ","), k.RefSchema, k.RefTable, strings.Join(k.RefColumns, ",")))
	}
	want := []string{
		"a_mandate: mandate_id -> " + testSchema + ".mandates id",
		"b_branch: branch_region,branch_code -> " + testSchema + ".Branch region,Code",
		"c_parent: parent_id -> " + testSchema + ".orders id",
		"d_event: event_id,event_at -> " + testSchema + ".events id,at",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if keys[1].RefQualifiedName != testSchema+`."Branch"` || keys[0].RefQualifiedName != testSchema+".mandates" {
		t.Errorf("qualified names: %q, %q", keys[0].RefQualifiedName, keys[1].RefQualifiedName)
	}

	// A table with none: an empty list, not nil (the frontend maps over it).
	none, err := s.ForeignKeys(t.Context(), testSchema, "mandates")
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("mandates: %v, %v", none, err)
	}
}

// A partition shows the foreign key it inherits from its parent table.
func TestForeignKeysOnPartition(t *testing.T) {
	s := editFixture(t,
		`CREATE TABLE `+testSchema+`.accounts (id int PRIMARY KEY)`,
		`CREATE TABLE `+testSchema+`.payments (id int, account_id int REFERENCES `+testSchema+`.accounts, at date) PARTITION BY RANGE (at)`,
		`CREATE TABLE `+testSchema+`.payments_2026 PARTITION OF `+testSchema+`.payments FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')`,
	)
	for _, table := range []string{"payments", "payments_2026"} {
		keys, err := s.ForeignKeys(t.Context(), testSchema, table)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || keys[0].RefTable != "accounts" || !slices.Equal(keys[0].Columns, []string{"account_id"}) {
			t.Errorf("%s: %+v", table, keys)
		}
	}
}

func TestForeignKeysNotConnected(t *testing.T) {
	if _, err := (&DBService{}).ForeignKeys(t.Context(), "public", "x"); err == nil {
		t.Fatal("expected an error when not connected")
	}
}
