package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// RevealedMarker replaces a revealed secret value in text that a run
// records: an assistant message, the arguments of a tool call or a tool
// result.
const RevealedMarker = "[secret value]"

// MaskText returns s with each value of revealed replaced by
// RevealedMarker. It replaces the longest value first, so a value that
// holds another value is masked whole. An empty value masks nothing.
func MaskText(s string, revealed []string) string {
	for _, v := range longestFirst(revealed) {
		s = strings.ReplaceAll(s, v, RevealedMarker)
	}

	return s
}

// MaskJSON returns raw with each value of revealed masked in each string of
// the document, and each number whose text is a value of revealed replaced
// by RevealedMarker. Object keys are masked too. It returns ErrInvalid for
// raw that is not JSON. Empty raw stays empty.
func MaskJSON(raw json.RawMessage, revealed []string) (json.RawMessage, error) {
	if len(raw) == 0 || len(revealed) == 0 {
		return raw, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: mask a document that is not JSON", ErrInvalid)
	}

	out, err := json.Marshal(maskValue(doc, longestFirst(revealed)))
	if err != nil {
		return nil, fmt.Errorf("mask a document: %w", err)
	}

	return out, nil
}

// MaskMessage returns m with each value of revealed masked in its text
// parts, the arguments of its tool calls and the content of its tool
// results. The loop records only a masked message.
func MaskMessage(m Message, revealed []string) (Message, error) {
	parts := make([]Part, 0, len(m.Parts))

	for i := range m.Parts {
		p := m.Parts[i]
		p.Text = MaskText(p.Text, revealed)

		args, err := MaskJSON(p.Call.Arguments, revealed)
		if err != nil {
			return Message{Role: m.Role, Parts: nil}, err
		}

		content, err := MaskJSON(p.Result.Content, revealed)
		if err != nil {
			return Message{Role: m.Role, Parts: nil}, err
		}

		p.Call.Arguments, p.Result.Content = args, content
		parts = append(parts, p)
	}

	return Message{Role: m.Role, Parts: parts}, nil
}

func maskValue(v any, revealed []string) any {
	switch x := v.(type) {
	case string:
		return MaskText(x, revealed)
	case json.Number:
		if slices.Contains(revealed, x.String()) {
			return RevealedMarker
		}

		return x
	case []any:
		for i := range x {
			x[i] = maskValue(x[i], revealed)
		}

		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[MaskText(k, revealed)] = maskValue(e, revealed)
		}

		return out
	default:
		return v
	}
}

// longestFirst returns the non-empty values of revealed, longest first.
func longestFirst(revealed []string) []string {
	out := slices.DeleteFunc(slices.Clone(revealed), func(v string) bool { return v == "" })
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(len(b), len(a)) })

	return out
}

// SealedReveal holds the result of one tool call with secret values in
// plain text, from the tools step that read them until the next model step
// has sent them to the model. A RevealSealer seals Content and Values with a
// data key of their own, and wraps the data key with the key-encryption
// key. The store keeps it in its own table, never in the transcript. The
// model step of the next turn opens it, also on another instance after a
// takeover. The record of that model step shreds it: the store deletes the
// wrapped key and the content in the transaction of the record. Values holds
// the plain values, sealed in the same way, so the model step can mask its
// answer.
//
// ExpiresAt is the hard end of the reveal: At plus the time limit of the run
// (RevealExpiry). A run never lives longer than its time limit, so a reveal
// never outlives its run, also when no step of the run ever shreds it.
// Reveals answers no reveal after its ExpiresAt, and SweepReveals deletes
// it.
type SealedReveal struct {
	Run        RunID
	Turn       int
	CallID     string
	WrappedKey []byte
	Content    []byte
	Values     []byte
	At         time.Time
	ExpiresAt  time.Time
}

// RevealExpiry returns the hard end of a reveal made at the time at by a
// run with limits l: at plus l.MaxDuration.
func RevealExpiry(at time.Time, l Limits) time.Time {
	return at.Add(l.MaxDuration)
}

// PlainReveal is a reveal before its seal and after its open. Content is the
// tool result with the plain values, and Values lists them.
type PlainReveal struct {
	Run       RunID
	Turn      int
	CallID    string
	Content   []byte
	Values    []string
	At        time.Time
	ExpiresAt time.Time
}

// ErrRevealOpen reports a sealed reveal that its sealer cannot open: its
// key, its content or the run, turn or call that it names changed after the
// seal.
var ErrRevealOpen = errors.New("the sealed reveal cannot be opened")

// RevealSealer seals and opens reveals. Package secret implements it: Seal
// makes a fresh data key for each reveal, seals Content and Values with it,
// and wraps the data key with the Wrapper of the key-encryption key. The run,
// the turn and the call are the additional data of each seal, so a sealed
// reveal opens only for the call that made it. The sealer keeps no key and
// no plain value after a call returns. Each implementation runs the
// conformance suite in package agenttest.
type RevealSealer interface {
	// Seal returns p sealed. It returns ErrInvalid for a reveal with no run,
	// call, content or value, or with ExpiresAt not after At.
	Seal(ctx context.Context, p PlainReveal) (SealedReveal, error)
	// Open returns the plain reveal of s. It returns ErrRevealOpen for a
	// reveal that changed after its seal, and never a part of the content
	// in an error.
	Open(ctx context.Context, s SealedReveal) (PlainReveal, error)
}
