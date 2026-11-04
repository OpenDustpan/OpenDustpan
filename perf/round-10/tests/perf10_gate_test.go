package stategate_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

type perf10Guard struct{}

func (perf10Guard) GuardStateWrite(context.Context, gate.StateWrite) (gate.Decision, error) {
	return gate.Decision{Record: "rec", Result: gate.ResultAllow}, nil
}

func perf10Write(b *testing.B) stategate.Write {
	unit, err := identity.UnitObject("pulumi", "acme", "web/dev")
	if err != nil {
		b.Fatalf("unit: %v", err)
	}
	return stategate.Write{
		Principal: identity.Principal{}, Unit: unit, Operation: gate.OperationStateImport,
		Action: identity.ActionUnitWrite, Source: gate.SourceCLI,
	}
}

// BenchmarkPerf10AdmitAllow: the Admitter with a guard that allows (the cost of Admit itself, no store).
func BenchmarkPerf10AdmitAllow(b *testing.B) {
	a, err := stategate.New(perf10Guard{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		b.Fatal(err)
	}
	w := perf10Write(b)
	rec := func(context.Context, string) {}
	b.ReportAllocs()
	for b.Loop() {
		if out := a.Admit(context.Background(), w, rec); !out.Allowed {
			b.Fatalf("refused: %+v", out)
		}
	}
}

// BenchmarkPerf10AdmitOff: the Off guard (store keeps no gates).
func BenchmarkPerf10AdmitOff(b *testing.B) {
	a, err := stategate.New(stategate.Off("benchmark"), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		b.Fatal(err)
	}
	w := perf10Write(b)
	rec := func(context.Context, string) {}
	b.ReportAllocs()
	for b.Loop() {
		if out := a.Admit(context.Background(), w, rec); !out.Allowed {
			b.Fatalf("refused: %+v", out)
		}
	}
}
