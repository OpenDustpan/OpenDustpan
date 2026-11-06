package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

// pgError returns a server error with the SQLSTATE code.
func pgError(code string) *pgconn.PgError {
	e := new(pgconn.PgError)
	e.Code = code

	return e
}

// translateCase is one error and the class that translate must give it. An
// empty class means that the error passes through.
type translateCase struct {
	name string
	err  error
	want storeerr.Class
}

// translateCases returns the table of TestTranslate. connectErr is a real
// connect error.
func translateCases(connectErr error) []translateCase {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Source: nil, Addr: nil, Err: syscall.ECONNREFUSED}

	return []translateCase{
		{"connect error", connectErr, storeerr.ConnectionFailed},
		{"dial error", fmt.Errorf("query: %w", dialErr), storeerr.ConnectionFailed},
		{"connection reset", fmt.Errorf("query: %w", syscall.ECONNRESET), storeerr.ConnectionFailed},
		{"unexpected eof", fmt.Errorf("query: %w", io.ErrUnexpectedEOF), storeerr.ConnectionFailed},
		{"connection exception", pgError("08000"), storeerr.ConnectionFailed},
		{"unable to establish", pgError("08001"), storeerr.ConnectionFailed},
		{"connection does not exist", pgError("08003"), storeerr.ConnectionFailed},
		{"establishment rejected", pgError("08004"), storeerr.ConnectionFailed},
		{"connection failure", pgError("08006"), storeerr.ConnectionFailed},
		{"transaction resolution unknown", pgError("08007"), storeerr.ConnectionFailed},
		{"too many connections", pgError("53300"), storeerr.Capacity},
		{"protocol violation is a bug", pgError("08P01"), ""},
		{"out of memory stays an error", pgError("53200"), ""},
		{"disk full stays an error", pgError("53100"), ""},
		{"admin shutdown", pgError("57P01"), storeerr.Shutdown},
		{"crash shutdown", pgError("57P02"), storeerr.Shutdown},
		{"cannot connect now", pgError("57P03"), storeerr.Shutdown},
		{"read only transaction", pgError("25006"), storeerr.ReadOnly},
		{"statement timeout", pgError("57014"), storeerr.StatementTimeout},
		{"wrapped class", fmt.Errorf("transaction: %w", pgError("57P01")), storeerr.Shutdown},
		{"unique violation", pgError("23505"), ""},
		{"similar prefix", pgError("0A000"), ""},
		{"no rows", pgx.ErrNoRows, ""},
		{"canceled", context.Canceled, ""},
		{"caller deadline", context.DeadlineExceeded, ""},
	}
}

func TestTranslate(t *testing.T) {
	t.Parallel()

	// A real connect error: nothing listens on port 1.
	_, connectErr := pgconn.Connect(t.Context(), "postgres://u@127.0.0.1:1/d?sslmode=disable&connect_timeout=2")

	if asConnect, ok := errors.AsType[*pgconn.ConnectError](connectErr); !ok || asConnect == nil {
		t.Fatalf("connect to a closed port: err = %v, want a ConnectError", connectErr)
	}

	for _, tc := range translateCases(connectErr) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkTranslation(t, tc)
		})
	}

	if got := translate(nil); got != nil {
		t.Fatalf("translate(nil) = %v, want nil", got)
	}
}

// checkTranslation checks the answer of translate for one case.
func checkTranslation(t *testing.T, tc translateCase) {
	t.Helper()

	got := translate(tc.err)

	if tc.want == "" {
		if !errors.Is(got, tc.err) || errors.Is(got, namespace.ErrUnavailable) {
			t.Fatalf("translate(%v) = %v, want the same error and no ErrUnavailable", tc.err, got)
		}

		return
	}

	var unavailable *storeerr.Error
	if !errors.As(got, &unavailable) || !errors.Is(got, namespace.ErrUnavailable) || unavailable.Class() != tc.want {
		t.Fatalf("translate(%v) = %v, want ErrUnavailable of class %q", tc.err, got, tc.want)
	}

	if !errors.Is(got, tc.err) {
		t.Fatalf("translate(%v) lost the driver error", tc.err)
	}

	if errors.Is(got, namespace.ErrUnreachable) != tc.want.Unreachable() {
		t.Fatalf("translate(%v) wraps ErrUnreachable = %t, want %t", tc.err, !tc.want.Unreachable(),
			tc.want.Unreachable())
	}

	if again := translate(got); !errors.Is(again, got) || again.Error() != got.Error() {
		t.Fatalf("translate twice wrapped the error again: %v", again)
	}
}
