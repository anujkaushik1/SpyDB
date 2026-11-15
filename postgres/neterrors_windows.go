//go:build windows

package postgres

import "golang.org/x/sys/windows"

// A refused or unreachable connection, as Windows reports it: Winsock's
// numbers, not the ones syscall names (describeConnectError).
var (
	connRefused     = []error{windows.WSAECONNREFUSED}
	hostUnreachable = []error{windows.WSAEHOSTUNREACH, windows.WSAENETUNREACH}
)
