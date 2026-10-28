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

func TestTranslate(t *testing.T) {
	t.Parallel()

	dialErr := &net.OpError{Op: "dial", Net: "tcp", Source: nil, Addr: nil, Err: syscall.ECONNREFUSED}

	// A real connect error: nothing listens on port 1.
	_, connectErr := pgconn.Connect(t.Context(), "postgres://u@127.0.0.1:1/d?sslmode=disable&connect_timeout=2")
	if _, ok := errors.AsType[*pgconn.ConnectError](connectErr); !ok {
		t.Fatalf("connect to a closed port: err = %v, want a ConnectError", connectErr)
	}

	tests := []struct {
		name string
		err  error
		want storeerr.Class // empty: the error passes through
	}{
		{"connect error", connectErr, storeerr.ConnectionFailed},
		{"dial error", fmt.Errorf("query: %w", dialErr), storeerr.ConnectionFailed},
		{"connection reset", fmt.Errorf("query: %w", syscall.ECONNRESET), storeerr.ConnectionFailed},
		{"unexpected eof", fmt.Errorf("query: %w", io.ErrUnexpectedEOF), storeerr.ConnectionFailed},
		{"connection exception class", &pgconn.PgError{Code: "08006"}, storeerr.ConnectionFailed},
		{"connection failure", &pgconn.PgError{Code: "08000"}, storeerr.ConnectionFailed},
		{"admin shutdown", &pgconn.PgError{Code: "57P01"}, storeerr.Shutdown},
		{"crash shutdown", &pgconn.PgError{Code: "57P02"}, storeerr.Shutdown},
		{"cannot connect now", &pgconn.PgError{Code: "57P03"}, storeerr.Shutdown},
		{"read only transaction", &pgconn.PgError{Code: "25006"}, storeerr.ReadOnly},
		{"wrapped class", fmt.Errorf("transaction: %w", &pgconn.PgError{Code: "57P01"}), storeerr.Shutdown},
		{"unique violation", &pgconn.PgError{Code: "23505"}, ""},
		{"similar prefix", &pgconn.PgError{Code: "0A000"}, ""},
		{"no rows", pgx.ErrNoRows, ""},
		{"canceled", context.Canceled, ""},
		{"caller deadline", context.DeadlineExceeded, ""},
		{"nil", nil, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := translate(tc.err)

			if tc.want == "" {
				if tc.err != nil && !errors.Is(got, tc.err) {
					t.Fatalf("translate(%v) = %v, want the same error", tc.err, got)
				}

				if tc.err == nil && got != nil {
					t.Fatalf("translate(%v) = %v, want the same error", tc.err, got)
				}

				if errors.Is(got, namespace.ErrUnavailable) {
					t.Fatalf("translate(%v) is ErrUnavailable, want a pass-through", tc.err)
				}

				return
			}

			unavailable, ok := errors.AsType[*storeerr.Error](got)
			if !ok || !errors.Is(got, namespace.ErrUnavailable) || unavailable.Class() != tc.want {
				t.Fatalf("translate(%v) = %v, want ErrUnavailable of class %q", tc.err, got, tc.want)
			}

			if !errors.Is(got, tc.err) {
				t.Fatalf("translate(%v) lost the driver error", tc.err)
			}

			if again := translate(got); !errors.Is(again, got) || again.Error() != got.Error() {
				t.Fatalf("translate twice wrapped the error again: %v", again)
			}
		})
	}
}
