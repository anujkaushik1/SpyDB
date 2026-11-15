//go:build !windows

package postgres

import "syscall"

// A refused or unreachable connection, as macOS and Linux report it
// (describeConnectError).
var (
	connRefused     = []error{syscall.ECONNREFUSED}
	hostUnreachable = []error{syscall.EHOSTUNREACH, syscall.ENETUNREACH}
)
