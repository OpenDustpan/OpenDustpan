package syslog_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/syslog"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// worstCaseEntry builds an entry with every capped scalar field at its cap
// and MaxChanges compact changes: the largest record the audit bounds still
// let through.
func worstCaseEntry() audit.Entry {
	e := audit.Entry{
		At:           time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Namespace:    "acme",
		Subject:      strings.Repeat("s", audit.MaxSubjectLen),
		IDPSubject:   strings.Repeat("i", audit.MaxIDPSubjectLen),
		TokenID:      "tok-aaaaaaaaaaaaaaaaaaaa",
		Action:       "unit:read-secrets",
		Object:       strings.Repeat("o", audit.MaxObjectLen),
		Version:      "sv-aaaaaaaaaaaaaaaaaaaa",
		ValuePath:    strings.Repeat("v", audit.MaxValuePathLen),
		ValueCount:   nil,
		SourceIP:     "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		UserAgent:    strings.Repeat("u", audit.MaxUserAgentLen),
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindReveal,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "0123456789abcdef0123456789abcdef",
		ObjectID:     "stk-aaaaaaaaaaaaaaaaaaaa",
		PrevHash:     strings.Repeat("p", 64),
		Hash:         strings.Repeat("h", 64),
		Sequence:     7,
		FenceVersion: 0,
	}

	for i := range audit.MaxChanges {
		e.Changes = append(e.Changes, audit.Change{Key: fmt.Sprintf("k%03d", i), New: "v"})
	}

	return e
}

// TestWorstCaseEntryFitsTheDefaultCap proves that an entry with every field
// at its maximum allowed length still ships: syslog is the only sink with a
// record cap, and its default cap holds the worst case after the standard
// trim. No record is skipped and no marker is sent.
func TestWorstCaseEntryFitsTheDefaultCap(t *testing.T) {
	t.Parallel()

	r := listenStream(t, nil)

	s, err := syslog.New(withMaxBytes(config(syslog.TCP, r.addr), syslog.DefaultMaxMessageBytes))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	if err := s.Write(t.Context(), []logsink.Record{audit.ToRecord(worstCaseEntry())}); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgs := r.waitFor(t, func(msgs [][]byte) bool { return len(msgs) > 0 })
	if len(msgs) != 1 {
		t.Fatalf("received %d messages, want 1", len(msgs))
	}

	if len(msgs[0]) > syslog.DefaultMaxMessageBytes {
		t.Fatalf("message holds %d bytes, want at most %d", len(msgs[0]), syslog.DefaultMaxMessageBytes)
	}

	fields := decodeBody(t, body(t, msgs[0]))

	if _, found := fields[syslog.SkippedKey]; found {
		t.Fatalf("worst case entry shipped as a marker: %s", body(t, msgs[0]))
	}

	if fields["hash"] != strings.Repeat("h", 64) {
		t.Fatalf("shipped entry lacks its hash: %s", body(t, msgs[0]))
	}
}

// sizeFeed is an in-memory audit.Feed and audit.NamespaceLister with one
// namespace.
type sizeFeed struct {
	mu         sync.Mutex
	entries    map[namespace.Name][]audit.Entry
	cursors    map[string]int64
	namespaces []namespace.Namespace
}

func newSizeFeed(entries []audit.Entry) *sizeFeed {
	return &sizeFeed{
		mu:      sync.Mutex{},
		entries: map[namespace.Name][]audit.Entry{"acme": entries},
		cursors: map[string]int64{},
		namespaces: []namespace.Namespace{{
			CreatedAt:       time.Unix(0, 0),
			Name:            "acme",
			HomeRegion:      "eu-west",
			FailoverVersion: 1,
		}},
	}
}

func (f *sizeFeed) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]namespace.Namespace(nil), f.namespaces...), nil
}

func (f *sizeFeed) After(_ context.Context, ns namespace.Name, after int64, limit int) ([]audit.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []audit.Entry

	for _, e := range f.entries[ns] {
		if e.Sequence > after && len(out) < limit {
			out = append(out, e)
		}
	}

	return out, nil
}

func (f *sizeFeed) SinkCursor(_ context.Context, sink string, ns namespace.Name) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.cursors[sink+"/"+ns.String()], nil
}

func (f *sizeFeed) AdvanceSinkCursor(_ context.Context, fence namespace.Fence, sink string, seq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := sink + "/" + fence.Namespace.String()
	if seq > f.cursors[key] {
		f.cursors[key] = seq
	}

	return nil
}

func (f *sizeFeed) cursor(sink string, ns namespace.Name) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.cursors[sink+"/"+ns.String()]
}

// TestSkippedRecordProducesMarkerAndCountsSkip proves that a record the sink
// cannot hold is never lost silently: with the message cap at its minimum,
// the worst case entry cannot fit even trimmed, so the sink sends a small
// marker with its scope, sequence and hash plus "skipped": "size", and the
// shipper still counts the skip.
func TestSkippedRecordProducesMarkerAndCountsSkip(t *testing.T) {
	t.Parallel()

	r := listenStream(t, nil)

	s, err := syslog.New(withMaxBytes(config(syslog.TCP, r.addr), syslog.MinMaxMessageBytes))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	entry := worstCaseEntry()

	feed := newSizeFeed([]audit.Entry{entry})

	var skipped int

	shipper, err := audit.NewShipper(audit.ShipperConfig{
		Feed:       feed,
		Namespaces: feed,
		Logger:     nil,
		OnShipped:  nil,
		OnFailure:  nil,
		OnSkipped:  func(_ string, n int) { skipped += n },
		OnLag:      nil,
		Now:        nil,
		Sinks:      []audit.ShipSink{{Sink: s, Name: "siem", Kind: "syslog", Interval: 0}},
		InstanceID: "",
		Leases:     nil,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	if err := shipper.ShipOnce(t.Context()); err != nil {
		t.Fatalf("ship once: %v", err)
	}

	if skipped != 1 {
		t.Fatalf("skipped count: got %d, want 1", skipped)
	}

	if got := feed.cursor("siem", "acme"); got != entry.Sequence {
		t.Fatalf("cursor after a skip: got %d, want %d", got, entry.Sequence)
	}

	msgs := r.waitFor(t, func(msgs [][]byte) bool { return len(msgs) > 0 })
	if len(msgs) != 1 {
		t.Fatalf("received %d messages, want the one marker", len(msgs))
	}

	if len(msgs[0]) > syslog.MinMaxMessageBytes {
		t.Fatalf("marker holds %d bytes, want at most %d", len(msgs[0]), syslog.MinMaxMessageBytes)
	}

	fields := decodeBody(t, body(t, msgs[0]))

	for key, want := range map[string]any{
		"scope":    "acme",
		"sequence": float64(entry.Sequence),
		"hash":     entry.Hash,
		"skipped":  syslog.SkippedSize,
	} {
		if fields[key] != want {
			t.Fatalf("marker field %q: got %v, want %v", key, fields[key], want)
		}
	}

	for _, key := range []string{"subject", "object", "valuePath", "userAgent", "truncated"} {
		if _, found := fields[key]; found {
			t.Fatalf("marker carries %q, want only scope, sequence, hash and skipped: %s", key, body(t, msgs[0]))
		}
	}

	if !bytes.Contains(body(t, msgs[0]), []byte(`"msg":"audit entry"`)) {
		t.Fatalf("marker lacks the audit message: %s", body(t, msgs[0]))
	}
}
