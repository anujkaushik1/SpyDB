package dynamo

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeDynamo is a DynamoDB holding tables (sorted), answering ListTables
// pageSize names at a time as AWS does, or refusing every call with
// refuse (an AWS error type) if set. It records each call's Authorization
// header: which key signed it, for which region.
func fakeDynamo(t *testing.T, tables []string, pageSize int, refuse string) (url string, calls func() []string) {
	t.Helper()
	var mu sync.Mutex
	var auth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		reply := func(status int, body string) {
			// Real DynamoDB sends its body's CRC32; the library checks it.
			w.Header().Set("X-Amz-Crc32", fmt.Sprint(crc32.ChecksumIEEE([]byte(body))))
			w.WriteHeader(status)
			fmt.Fprint(w, body)
		}
		if target := r.Header.Get("X-Amz-Target"); target != "DynamoDB_20120810.ListTables" {
			reply(400, fmt.Sprintf(`{"__type":"UnknownOperationException","message":"not faked: %s"}`, target))
			return
		}
		if refuse != "" {
			reply(400, fmt.Sprintf(`{"__type":"com.amazonaws.dynamodb.v20120810#%s","message":"User: arn:aws:iam::111122223333:user/you is not authorized to perform: dynamodb:ListTables"}`, refuse))
			return
		}
		var in struct{ ExclusiveStartTableName string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("ListTables body: %v", err)
		}
		start := 0
		if in.ExclusiveStartTableName != "" {
			start = slices.Index(tables, in.ExclusiveStartTableName) + 1
		}
		end := min(start+pageSize, len(tables))
		out := map[string]any{"TableNames": tables[start:end]}
		if end < len(tables) {
			out["LastEvaluatedTableName"] = tables[end-1]
		}
		body, _ := json.Marshal(out)
		reply(200, string(body))
	}))
	t.Cleanup(server.Close)
	return server.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(auth)
	}
}

const tablesConfig = `[profile orders]
region = ap-south-1

[profile noregion]
`

const tablesCredentials = `[orders]
aws_access_key_id = AKIAORDERSEXAMPLE
aws_secret_access_key = orders-secret

[noregion]
aws_access_key_id = AKIANOREGIONEXAMPLE
aws_secret_access_key = noregion-secret
`

func tableNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("table-%03d", i)
	}
	return names
}

func TestListTables(t *testing.T) {
	awsFiles(t, tablesConfig, tablesCredentials)
	// Keys in SpyDB's own environment must not be used instead of the profile's.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAENVIRONMENTEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")
	t.Setenv("AWS_PROFILE", "noregion")

	t.Run("every page, signed with the profile's key, in its region", func(t *testing.T) {
		tables := tableNames(250)
		url, calls := fakeDynamo(t, tables, 100, "")
		s := &Service{endpoint: url}
		conn, err := s.Connect(t.Context(), "orders")
		if err != nil || conn != (Connection{Profile: "orders", Region: "ap-south-1"}) {
			t.Fatalf("Connect: %+v, %v", conn, err)
		}
		if n := len(calls()); n != 0 {
			t.Fatalf("Connect called AWS %d times; it shouldn't call it at all", n)
		}
		got, err := s.ListTables(t.Context())
		if err != nil || !slices.Equal(got, tables) {
			t.Fatalf("got %d tables (%v), want %d", len(got), err, len(tables))
		}
		auth := calls()
		if len(auth) != 3 {
			t.Fatalf("%d calls for 250 tables at 100 a page, want 3", len(auth))
		}
		for _, a := range auth {
			if !strings.Contains(a, "Credential=AKIAORDERSEXAMPLE/") || !strings.Contains(a, "/ap-south-1/dynamodb/aws4_request") {
				t.Errorf("signed as %q, want the orders key for ap-south-1", a)
			}
		}
	})

	t.Run("a profile with no region: us-east-1", func(t *testing.T) {
		url, calls := fakeDynamo(t, tableNames(2), 100, "")
		s := &Service{endpoint: url}
		conn, err := s.Connect(t.Context(), "noregion")
		if err != nil || conn.Region != "us-east-1" {
			t.Fatalf("Connect: %+v, %v", conn, err)
		}
		if _, err := s.ListTables(t.Context()); err != nil {
			t.Fatal(err)
		}
		if a := calls()[0]; !strings.Contains(a, "Credential=AKIANOREGIONEXAMPLE/") || !strings.Contains(a, "/us-east-1/dynamodb/") {
			t.Errorf("signed as %q", a)
		}
	})

	t.Run("no tables: an empty list, not nothing", func(t *testing.T) {
		url, _ := fakeDynamo(t, nil, 100, "")
		s := &Service{endpoint: url}
		if _, err := s.Connect(t.Context(), "orders"); err != nil {
			t.Fatal(err)
		}
		got, err := s.ListTables(t.Context())
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("got %#v, %v", got, err)
		}
	})

	t.Run("not allowed: AWS's reason", func(t *testing.T) {
		url, _ := fakeDynamo(t, tableNames(2), 100, "AccessDeniedException")
		s := &Service{endpoint: url}
		if _, err := s.Connect(t.Context(), "orders"); err != nil {
			t.Fatal(err)
		}
		_, err := s.ListTables(t.Context())
		if err == nil || !strings.HasPrefix(err.Error(), "AWS says this profile isn't allowed to do that: ") || !strings.Contains(err.Error(), "dynamodb:ListTables") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("keys AWS doesn't know: sign in again", func(t *testing.T) {
		url, _ := fakeDynamo(t, tableNames(2), 100, "UnrecognizedClientException")
		s := &Service{endpoint: url}
		if _, err := s.Connect(t.Context(), "orders"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ListTables(t.Context()); err == nil || !strings.Contains(err.Error(), "Disconnect and sign in again.") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("not connected, or disconnected: nothing goes to AWS", func(t *testing.T) {
		url, calls := fakeDynamo(t, tableNames(2), 100, "")
		s := &Service{endpoint: url}
		if _, err := s.ListTables(t.Context()); err == nil {
			t.Fatal("listed tables without connecting")
		}
		if _, err := s.Connect(t.Context(), "orders"); err != nil {
			t.Fatal(err)
		}
		s.Disconnect()
		if _, err := s.ListTables(t.Context()); err == nil {
			t.Fatal("listed tables after disconnecting")
		}
		if n := len(calls()); n != 0 {
			t.Fatalf("%d calls to AWS", n)
		}
	})

	t.Run("a profile that isn't there", func(t *testing.T) {
		_, err := (&Service{}).Connect(t.Context(), "nope")
		if err == nil || !strings.Contains(err.Error(), `"nope"`) {
			t.Fatalf("got %v", err)
		}
	})
}

// The real thing, read-only: lists the tables a profile of yours can see.
// Only with SPYDB_AWS_PROFILE set to that profile's name.
func TestListTablesFromAWS(t *testing.T) {
	profile := os.Getenv("SPYDB_AWS_PROFILE")
	if profile == "" {
		t.Skip("set SPYDB_AWS_PROFILE to one of your AWS profiles (lists its DynamoDB tables; changes nothing)")
	}
	s := &Service{}
	conn, err := s.Connect(t.Context(), profile)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := s.ListTables(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s in %s: %d tables", conn.Profile, conn.Region, len(tables))
}
