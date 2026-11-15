package mongodb

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestSignIn checks the form's fields (user, password, auth database) against
// a server that wants a sign-in: SPYDB_MONGO_AUTH as host:port:user:password,
// e.g. a mongo:8.0 container started with MONGO_INITDB_ROOT_USERNAME/PASSWORD.
func TestSignIn(t *testing.T) {
	spec := os.Getenv("SPYDB_MONGO_AUTH")
	if spec == "" {
		t.Skip("set SPYDB_MONGO_AUTH=host:port:user:password to test signing in")
	}
	parts := strings.SplitN(spec, ":", 4)
	if len(parts) != 4 {
		t.Fatal("SPYDB_MONGO_AUTH is host:port:user:password")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	port, _ := strconv.Atoi(parts[1])
	good := Config{Host: parts[0], Port: port, User: parts[2], Password: parts[3], AuthSource: "admin", Database: "spydb_test_auth"}
	s := &Service{}
	defer s.Disconnect()

	msg, err := s.TestConnection(good)
	if err != nil || !strings.Contains(msg, "as "+parts[2]) {
		t.Fatalf("test with the right password: %q %v", msg, err)
	}
	wrong := good
	wrong.Password = "nope"
	if _, err := s.TestConnection(wrong); err == nil || !strings.Contains(err.Error(), "didn’t accept the user and password") {
		t.Fatalf("wrong password: %v", err)
	}
	wrongSource := good
	wrongSource.AuthSource = "spydb_test_auth"
	if _, err := s.Connect(wrongSource); err == nil {
		t.Fatal("the wrong auth database should be refused")
	}
	info, err := s.Connect(good)
	if err != nil || info.User != parts[2] || info.Database != "spydb_test_auth" {
		t.Fatalf("connect: %+v %v", info, err)
	}
	// The same through a connection string.
	uri := Config{URI: "mongodb://" + parts[2] + ":" + parts[3] + "@" + parts[0] + ":" + parts[1] + "/?authSource=admin"}
	if info, err = s.Connect(uri); err != nil || info.User != parts[2] {
		t.Fatalf("connection string: %+v %v", info, err)
	}
	if info.Database == "" { // none named: the first of the server's own, else "test"
		t.Fatalf("no database picked: %+v", info)
	}
}
