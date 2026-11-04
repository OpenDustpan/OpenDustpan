package pulumi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/requestid"
)

var perf10Err = errors.New("driver: unclassified failure")

func benchWriteError(b *testing.B, h slog.Handler, withID bool) {
	hd := new(Handler)
	hd.logger = slog.New(h)
	ctx := context.Background()
	if withID {
		ctx = requestid.WithContext(ctx, "0123abcd0123abcd")
	}
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		hd.writeError(ctx, rec, perf10Err)
	}
}

// 500 error path of the Pulumi adapter, JSON log handler to a discard writer.
func BenchmarkPerf10WriteError500JSON(b *testing.B) {
	benchWriteError(b, slog.NewJSONHandler(io.Discard, nil), true)
}

func BenchmarkPerf10WriteError500Text(b *testing.B) {
	benchWriteError(b, slog.NewTextHandler(io.Discard, nil), true)
}

// Same path with the Error level off: the cost of everything but the line.
func BenchmarkPerf10WriteError500LogOff(b *testing.B) {
	benchWriteError(b, slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.Level(100)}), true)
}
