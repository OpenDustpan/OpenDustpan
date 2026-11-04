package faultlog_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/requestid"
)

func BenchmarkPerf10LogInternal(b *testing.B) {
	l := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ctx := requestid.WithContext(context.Background(), "0123abcd0123abcd")
	b.ReportAllocs()
	for b.Loop() {
		faultlog.LogInternal(ctx, l, "request failed", faultlog.CauseUnclassified, "server.internal")
	}
}

// The line that the adapters wrote before faultlog: status and code only.
func BenchmarkPerf10OldStyleLine(b *testing.B) {
	l := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ctx := requestid.WithContext(context.Background(), "0123abcd0123abcd")
	b.ReportAllocs()
	for b.Loop() {
		l.ErrorContext(ctx, "request failed", slog.Int("status", 500), slog.String("error_code", "server.internal"))
	}
}
