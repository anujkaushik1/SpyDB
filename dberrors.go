package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// ConnectionError is returned when a connection test fails. Wails JSON-encodes
// the error into the rejected promise's `cause`, so the UI shows Summary and
// keeps pgx's raw text as Detail.
type ConnectionError struct {
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

func (e *ConnectionError) Error() string { return e.Summary }

// Hints appended to Postgres server errors, keyed by SQLSTATE.
var pgErrorHints = map[string]string{
	"28P01": "Check the user and password.",
	"28000": "Check the user name.",
	"3D000": "Check the database name.",
}

// describeConnectError turns a pgx connect error into a plain sentence.
func describeConnectError(err error, cfg DBConfig) string {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		msg := sentence(pgErr.Message)
		if hint, ok := pgErrorHints[pgErr.Code]; ok {
			msg += " " + hint
		}
		return msg
	}

	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("Can't find host %q. Check the spelling.", cfg.Host)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Sprintf("Nothing is accepting connections on %s. Is Postgres running on that port?", addr)
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return fmt.Sprintf("Can't reach %s from this machine.", cfg.Host)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return fmt.Sprintf("Timed out connecting to %s. Check the host, port and any firewall.", addr)
	}
	return fmt.Sprintf("Couldn't connect to %s.", addr)
}

// sentence capitalises s and ends it with a full stop.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	r, size := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[size:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}
