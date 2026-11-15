package postgres

import (
	"net"
	"os"
	"strings"
	"testing"
)

// A refused or unreachable connection reads the same on every system, though
// each reports it with its own numbers (neterrors_*.go). No database needed.
func TestDescribeConnectErrorNetwork(t *testing.T) {
	cfg := DBConfig{Host: "db.example", Port: 5433}
	dial := func(err error) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", err)}
	}
	for _, err := range connRefused {
		if got := describeConnectError(dial(err), cfg); !strings.HasPrefix(got, "Nothing is accepting connections on db.example:5433.") {
			t.Errorf("%v: %q", err, got)
		}
	}
	for _, err := range hostUnreachable {
		if got := describeConnectError(dial(err), cfg); got != "Can't reach db.example from this machine." {
			t.Errorf("%v: %q", err, got)
		}
	}

	// For real: a port nothing listens on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err = net.Dial("tcp", ln.Addr().String())
	if got := describeConnectError(err, DBConfig{Host: "127.0.0.1", Port: port}); !strings.HasPrefix(got, "Nothing is accepting connections") {
		t.Errorf("closed port (%v): %q", err, got)
	}
}
