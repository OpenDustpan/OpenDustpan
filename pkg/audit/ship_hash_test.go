package audit_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/syslog"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// hashSinkReceiver collects the datagrams of a local UDP listener.
type hashSinkReceiver struct {
	addr string
	msgs [][]byte
	mu   sync.Mutex
}

// listenHashUDP starts a UDP receiver on 127.0.0.1 that stops with the test.
func listenHashUDP(t *testing.T) *hashSinkReceiver {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}

	r := &hashSinkReceiver{addr: pc.LocalAddr().String(), msgs: nil, mu: sync.Mutex{}}

	var wg sync.WaitGroup

	wg.Go(func() {
		buf := make([]byte, 64*1024)

		for {
			n, _, readErr := pc.ReadFrom(buf)
			if readErr != nil {
				return
			}

			r.mu.Lock()
			r.msgs = append(r.msgs, append([]byte(nil), buf[:n]...))
			r.mu.Unlock()
		}
	})

	t.Cleanup(func() {
		if err := pc.Close(); err != nil {
			t.Errorf("close udp listener: %v", err)
		}

		wg.Wait()
	})

	return r
}

// waitHashFor waits until want messages arrived, then answers a copy of them.
func (r *hashSinkReceiver) waitHashFor(t *testing.T, want int) [][]byte {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		r.mu.Lock()
		msgs := append([][]byte(nil), r.msgs...)
		r.mu.Unlock()

		if len(msgs) >= want || time.Now().After(deadline) {
			if len(msgs) < want {
				t.Fatalf("syslog messages = %d, want %d", len(msgs), want)
			}

			return msgs
		}
	}
}

// hashEntryWithRealHash answers an entry with the given long values and a
// hash computed over its fields, so a receiver can check it again.
func hashEntryWithRealHash(subject, object, userAgent, requestID string) audit.Entry {
	e := audit.Entry{
		At:           time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Namespace:    "acme",
		Subject:      subject,
		IDPSubject:   "idp-sub-1",
		TokenID:      "tok-1",
		Action:       "unit:read-secrets",
		Object:       object,
		Version:      "sv-1",
		ValuePath:    "/outputs/db/password",
		SourceIP:     "192.0.2.1",
		UserAgent:    userAgent,
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindReveal,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    requestID,
		ObjectID:     "stk-1",
		PrevHash:     "prev-hash-value",
		Hash:         "",
		Sequence:     7,
		FenceVersion: 1,
	}

	e.Hash = audit.ComputeHash(e, e.PrevHash)

	return e
}

// checkShippedHash recomputes the hash of the shipped JSON body from its
// fields and fails when it does not match the shipped hash.
func checkShippedHash(t *testing.T, body []byte) {
	t.Helper()

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode shipped body: %v", err)
	}

	var (
		scope      string
		subject    string
		idpSubject string
		tokenID    string
		action     string
		object     string
		version    string
		valuePath  string
		sourceIP   string
		userAgent  string
		channel    string
		kind       string
		outcome    string
		requestID  string
		prevHash   string
		hash       string
		objectID   string
		sequence   int64
		atMs       int64
		fence      int64
	)

	decodeString := func(key string, dst *string) {
		t.Helper()

		raw, ok := fields[key]
		if !ok {
			return
		}

		if err := json.Unmarshal(raw, dst); err != nil {
			t.Fatalf("decode field %q: %v", key, err)
		}
	}

	decodeInt := func(key string, dst *int64) {
		t.Helper()

		raw, ok := fields[key]
		if !ok {
			return
		}

		if err := json.Unmarshal(raw, dst); err != nil {
			t.Fatalf("decode field %q: %v", key, err)
		}
	}

	decodeString("scope", &scope)
	decodeString("subject", &subject)
	decodeString("idpSubject", &idpSubject)
	decodeString("tokenID", &tokenID)
	decodeString("action", &action)
	decodeString("object", &object)
	decodeString("version", &version)
	decodeString("valuePath", &valuePath)
	decodeString("sourceIP", &sourceIP)
	decodeString("userAgent", &userAgent)
	decodeString("channel", &channel)
	decodeString("kind", &kind)
	decodeString("outcome", &outcome)
	decodeString("requestID", &requestID)
	decodeString("prevHash", &prevHash)
	decodeString("hash", &hash)
	decodeString("objectID", &objectID)
	decodeInt("sequence", &sequence)
	decodeInt("atMs", &atMs)
	decodeInt("fenceVersion", &fence)

	changesText := ""
	if raw, ok := fields["changes"]; ok {
		changesText = string(raw)
	}

	var valueCount *int64
	if raw, ok := fields["valueCount"]; ok {
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatalf("decode field valueCount: %v", err)
		}

		valueCount = &n
	}

	if hash == "" {
		t.Fatal("shipped record holds no hash")
	}

	rebuilt := audit.Entry{
		At:           time.UnixMilli(atMs).UTC(),
		Namespace:    mustNamespace(t, scope),
		Subject:      subject,
		IDPSubject:   idpSubject,
		TokenID:      tokenID,
		Action:       action,
		Object:       object,
		Version:      version,
		ValuePath:    valuePath,
		ValueCount:   valueCount,
		SourceIP:     sourceIP,
		UserAgent:    userAgent,
		Channel:      audit.Channel(channel),
		Kind:         audit.Kind(kind),
		Outcome:      audit.Outcome(outcome),
		RequestID:    requestID,
		ObjectID:     objectID,
		PrevHash:     prevHash,
		Hash:         hash,
		Sequence:     sequence,
		FenceVersion: fence,
	}

	if got := audit.ComputeStoredHash(rebuilt, prevHash, changesText); got != hash {
		t.Fatalf("shipped hash does not match its fields: got %s, recomputed %s", hash, got)
	}
}

// mustNamespace parses a namespace name or fails the test.
func mustNamespace(t *testing.T, name string) namespace.Name {
	t.Helper()

	ns, err := namespace.ParseName(name)
	if err != nil {
		t.Fatalf("parse namespace %q: %v", name, err)
	}

	return ns
}

// hashBodyOf answers the MSG part of an RFC 5424 message: everything after
// the seventh space.
func hashBodyOf(t *testing.T, msg []byte) []byte {
	t.Helper()

	parts := bytes.SplitN(msg, []byte(" "), 8)
	if len(parts) != 8 {
		t.Fatalf("not an RFC 5424 message: %q", msg)
	}

	return parts[7]
}

// TestShippedEntryHashSurvivesSmallSinkLimit ships entries with long fields
// through a sink with a small limit and checks that each delivered record
// still carries a hash that recomputes from its shipped fields. A record
// that cannot fit even with every trimmable field empty is skipped and
// counted instead of delivered with a shortened hash.
func TestShippedEntryHashSurvivesSmallSinkLimit(t *testing.T) {
	t.Parallel()

	r := listenHashUDP(t)

	s, err := syslog.New(syslog.Config{
		TLSConfig:       nil,
		Network:         syslog.UDP,
		Address:         r.addr,
		Hostname:        "",
		AppName:         "",
		Facility:        0,
		Timeout:         0,
		MaxMessageBytes: 1024,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close sink: %v", err)
		}
	})

	plain := hashEntryWithRealHash("admin", "pulumi:acme/web/prod", "pulumi-cli/3.200.0", "req-7")

	if err := s.Write(t.Context(), []logsink.Record{audit.ToRecord(plain)}); err != nil {
		t.Fatalf("write plain entry: %v", err)
	}

	msgs := r.waitHashFor(t, 1)
	checkShippedHash(t, hashBodyOf(t, msgs[0]))

	long := hashEntryWithRealHash(
		strings.Repeat("s", 2000),
		strings.Repeat("o", 2000),
		strings.Repeat("u", 2000),
		strings.Repeat("r", 2000),
	)
	small := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "small", Attrs: nil}

	err = s.Write(t.Context(), []logsink.Record{audit.ToRecord(long), small})

	skip, ok := errors.AsType[*logsink.SkipError](err)
	if !ok || skip.Skipped != 1 || !errors.Is(err, logsink.ErrSkipped) {
		t.Fatalf("write long entry: got %v, want a skip of 1 record", err)
	}

	msgs = r.waitHashFor(t, 2)
	if len(msgs) != 2 || !bytes.Contains(msgs[1], []byte(`"msg":"small"`)) {
		t.Fatalf("received %q, want the plain entry and only the small record after it", msgs)
	}
}

// TestTrimmedAuditRecordHashMatchesFields trims a shipped entry with long
// fields to a small budget and checks that a delivered record still carries
// a hash that recomputes from its shipped fields, or that the record was
// skipped because only protected fields are long.
func TestTrimmedAuditRecordHashMatchesFields(t *testing.T) {
	t.Parallel()

	plain := hashEntryWithRealHash("admin", "pulumi:acme/web/prod", "pulumi-cli/3.200.0", "req-7")
	rec := audit.ToRecord(plain)

	line, err := logsink.MarshalJSON(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	trimmed, ok, err := logsink.TrimToFit(rec, len(line))
	if err != nil {
		t.Fatalf("trim: %v", err)
	}

	if !ok {
		t.Fatal("trim: reported no fit for a record within the limit")
	}

	trimmedLine, err := logsink.MarshalJSON(trimmed)
	if err != nil {
		t.Fatalf("marshal trimmed: %v", err)
	}

	checkShippedHash(t, trimmedLine)

	entry := hashEntryWithRealHash(
		strings.Repeat("s", 800),
		strings.Repeat("o", 800),
		strings.Repeat("u", 800),
		strings.Repeat("r", 800),
	)
	entry.Changes = []audit.Change{{Key: "region", Old: nil, New: strings.Repeat("e", 200), Layer: "file", TakesEffect: ""}}
	entry.Hash = audit.ComputeHash(entry, entry.PrevHash)

	longRec := audit.ToRecord(entry)

	if _, ok, err := logsink.TrimToFit(longRec, 480); err != nil {
		t.Fatalf("trim long entry: %v", err)
	} else if ok {
		t.Fatal("trim: reported fit for a record with only long protected fields")
	}
}
