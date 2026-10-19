package gatestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Records is gate.Records over a Runner.
type Records struct {
	run Runner
}

var _ gate.Records = (*Records)(nil)

// checkRecord returns gate.ErrInvalid for a record whose ID or namespace is
// not valid, or whose fence is not the fence of its namespace.
func checkRecord(r gate.Record, fence namespace.Fence) error {
	if _, err := gate.ParseRecordID(string(r.ID)); err != nil {
		return err
	}

	if r.Namespace != audit.ServerScope {
		if _, err := namespace.ParseName(r.Namespace.String()); err != nil {
			return fmt.Errorf("%w: record namespace: %w", gate.ErrInvalid, err)
		}
	}

	return checkFenceOf(r.Namespace, fence)
}

func recordRow(r gate.Record, refusalKey string) (RecordRow, error) {
	b, err := marshalRecord(&r)
	if err != nil {
		return RecordRow{}, fmt.Errorf("encode record %s: %w", r.ID, err)
	}

	return RecordRow{
		ID: string(r.ID), Point: string(r.Point), ChangeKind: string(r.Change.Kind), ChangeID: r.Change.ID,
		AtMs: r.At.UnixMilli(), LastAtMs: r.LastAt.UnixMilli(), Count: int64(r.Count), RefusalKey: refusalKey,
		Body: string(b),
	}, nil
}

func decodeRecord(body string) (gate.Record, error) {
	var r gate.Record
	if err := unmarshalRecord([]byte(body), &r); err != nil {
		return gate.Record{}, fmt.Errorf("decode record: %w", err)
	}

	return r, nil
}

func decodeRecords(bodies []string) ([]gate.Record, error) {
	out := make([]gate.Record, 0, len(bodies))

	for _, b := range bodies {
		r, err := decodeRecord(b)
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, nil
}

// Put implements gate.Records.
func (s *Records) Put(ctx context.Context, r gate.Record, fence namespace.Fence) error {
	if err := checkRecord(r, fence); err != nil {
		return err
	}

	row, err := recordRow(r, "")
	if err != nil {
		return err
	}

	return s.run.WriteRecord(ctx, r.Namespace, fence, func(tx Tx) error {
		return insertRecord(ctx, tx, r.Namespace, row)
	})
}

func insertRecord(ctx context.Context, tx Tx, ns namespace.Name, row RecordRow) error {
	exists, err := tx.RecordExists(ctx, row.ID)
	if err != nil {
		return err
	}

	if exists {
		return fmt.Errorf("%w: the record ID %s is used", gate.ErrConflict, row.ID)
	}

	return tx.InsertRecord(ctx, ns, row)
}

// refusalKey joins the refusals of one principal and one access limit: it is
// the digest of the subject, the action, the change, and the access limits that gave the
// refusal. A record holds no other name of the call. Each part has its length
// in front, so two lists of parts never give one key.
func refusalKey(r gate.Record) string {
	var limits []string

	for i := range r.Units {
		for j := range r.Units[i].Gates {
			limits = append(limits, r.Units[i].Gates[j].Policy.String())
		}
	}

	slices.Sort(limits)

	var b strings.Builder

	for _, part := range append([]string{r.Subject, r.Action, string(r.Change.Kind), r.Change.ID}, limits...) {
		b.WriteString(strconv.Itoa(len(part)))
		b.WriteByte(':')
		b.WriteString(part)
	}

	sum := sha256.Sum256([]byte(b.String()))

	return hex.EncodeToString(sum[:])
}

// NoteRefusal implements gate.Records.
func (s *Records) NoteRefusal(
	ctx context.Context, r gate.Record, window time.Duration, fence namespace.Fence,
) (gate.RecordID, error) {
	if err := checkRecord(r, fence); err != nil {
		return "", err
	}

	key := refusalKey(r)

	var id gate.RecordID

	err := s.run.WriteRecord(ctx, r.Namespace, fence, func(tx Tx) error {
		found, ok, err := tx.FindRefusal(ctx, r.Namespace, key, r.At.Add(-window))
		if err != nil {
			return err
		}

		if ok {
			id, err = coalesce(ctx, tx, found, r)

			return err
		}

		r.Count = 1
		r.LastAt = r.At
		id = r.ID

		row, err := recordRow(r, key)
		if err != nil {
			return err
		}

		return insertRecord(ctx, tx, r.Namespace, row)
	})
	if err != nil {
		return "", err
	}

	return id, nil
}

// coalesce adds the refusal r to the stored record found.
func coalesce(ctx context.Context, tx Tx, found RecordRow, r gate.Record) (gate.RecordID, error) {
	stored, err := decodeRecord(found.Body)
	if err != nil {
		return "", err
	}

	stored.Count++
	stored.LastAt = r.At

	b, err := marshalRecord(&stored)
	if err != nil {
		return "", fmt.Errorf("encode record %s: %w", stored.ID, err)
	}

	found.Count = int64(stored.Count)
	found.LastAtMs = stored.LastAt.UnixMilli()
	found.Body = string(b)

	if err := tx.UpdateRefusal(ctx, found); err != nil {
		return "", err
	}

	return stored.ID, nil
}

// Get implements gate.Records.
func (s *Records) Get(ctx context.Context, ns namespace.Name, id gate.RecordID) (gate.Record, error) {
	var out gate.Record

	err := s.run.Read(ctx, func(tx Tx) error {
		body, ok, err := tx.Record(ctx, ns, string(id))
		if err != nil {
			return err
		}

		if !ok {
			return fmt.Errorf("%w: record %s of %s", gate.ErrNotFound, id, ns)
		}

		out, err = decodeRecord(body)

		return err
	})
	if err != nil {
		return gate.Record{}, err
	}

	return out, nil
}

// ListByChange implements gate.Records.
func (s *Records) ListByChange(ctx context.Context, ns namespace.Name, change gate.ChangeRef) ([]gate.Record, error) {
	var out []gate.Record

	err := s.run.Read(ctx, func(tx Tx) error {
		bodies, err := tx.RecordsByChange(ctx, ns, string(change.Kind), change.ID)
		if err != nil {
			return err
		}

		out, err = decodeRecords(bodies)

		return err
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Query implements gate.Records.
func (s *Records) Query(ctx context.Context, ns namespace.Name, q gate.RecordQuery) ([]gate.Record, error) {
	if q.Limit < 1 || q.Limit > gate.MaxSimulateRecords {
		return nil, fmt.Errorf("%w: limit %d, give 1 to %d", gate.ErrInvalid, q.Limit, gate.MaxSimulateRecords)
	}

	var out []gate.Record

	err := s.run.Read(ctx, func(tx Tx) error {
		w := Window{Point: string(q.Point), From: q.From, To: q.To, Limit: q.Limit}

		bodies, err := tx.QueryRecords(ctx, ns, w)
		if err != nil {
			return err
		}

		out, err = decodeRecords(bodies)

		return err
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// marshalRecord returns the JSON form of r. gate.Record is the wire form of
// the record, and its types carry their own JSON names.
func marshalRecord(r *gate.Record) ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode record %s: %w", r.ID, err)
	}

	return b, nil
}

// unmarshalRecord reads the JSON form of a record into r.
func unmarshalRecord(b []byte, r *gate.Record) error {
	if err := json.Unmarshal(b, r); err != nil {
		return fmt.Errorf("decode record: %w", err)
	}

	return nil
}
