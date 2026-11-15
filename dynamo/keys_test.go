package dynamo

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
)

// Signing in with access keys against DynamoDB Local (SPYDB_DYNAMO_LOCAL),
// which takes any keys: the pair is checked, kept for Connect, saved to
// aws-keys.json in a settings folder of the test's own, listed without its
// secret, signed in with again by ID, and deleted.
func TestKeySignInOnDynamoDBLocal(t *testing.T) {
	url := os.Getenv("SPYDB_DYNAMO_LOCAL")
	if url == "" {
		t.Skip("set SPYDB_DYNAMO_LOCAL to a DynamoDB Local")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	s := &Service{endpoint: url}
	ctx := context.Background()

	if _, err := s.KeySignIn(ctx, AccessKeys{AccessKeyID: "AKIALOCALTEST", Region: "local"}); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("no secret: %v", err)
	}
	id, err := s.KeySignIn(ctx, AccessKeys{Name: "Local keys", AccessKeyID: "AKIALOCALTEST", SecretAccessKey: "secret", Region: "local", Save: true})
	if err != nil {
		t.Fatal(err)
	}
	if id.Profile != "keys:Local keys" || !strings.Contains(id.Arn, "AKIA…TEST") {
		t.Fatalf("identity %+v", id)
	}
	conn, err := s.Connect(ctx, id.Profile)
	if err != nil || conn.Region != "local" || conn.Profile != id.Profile {
		t.Fatalf("connect: %+v, %v", conn, err)
	}
	if _, err := s.ListTables(ctx); err != nil {
		t.Fatalf("list tables with the keys: %v", err)
	}

	saved, err := s.SavedAccessKeys()
	if err != nil || len(saved) != 1 || saved[0].Name != "Local keys" || saved[0].AccessKeyID != "AKIA…TEST" || saved[0].Region != "local" {
		t.Fatalf("saved: %+v, %v", saved, err)
	}
	file, _ := keysFile()
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), `"secretAccessKey": "secret"`) {
		t.Fatalf("the secret isn't in the file: %s", data)
	}
	// Windows has no file modes: the folder is in the user's profile, which Windows keeps to that user.
	if info, _ := os.Stat(file); runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("file readable by others: %v", info.Mode())
	}

	// Saving the same name again replaces it, not doubles it.
	if _, err := s.KeySignIn(ctx, AccessKeys{Name: "Local keys", AccessKeyID: "AKIALOCALTEST2", SecretAccessKey: "secret2", Region: "local", Save: true}); err != nil {
		t.Fatal(err)
	}
	saved, _ = s.SavedAccessKeys()
	if len(saved) != 1 || saved[0].AccessKeyID != "AKIA…EST2" {
		t.Fatalf("replaced: %+v", saved)
	}

	s.Disconnect()
	if _, err := s.Connect(ctx, id.Profile); err == nil {
		t.Fatal("Connect after Disconnect should need a sign-in first")
	}
	if _, err := s.SavedKeySignIn(ctx, saved[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connect(ctx, id.Profile); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSavedKeys(saved[0].ID); err != nil {
		t.Fatal(err)
	}
	if saved, _ = s.SavedAccessKeys(); len(saved) != 0 {
		t.Fatalf("not deleted: %+v", saved)
	}
	if _, err := s.SavedKeySignIn(ctx, "gone"); err == nil {
		t.Fatal("signing in with deleted keys should fail")
	}
}
