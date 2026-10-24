package stack_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

func TestCheckJournalSequenceIDs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		seqs []int64
		want error
	}{
		{name: "one ID", seqs: []int64{1}, want: nil},
		{name: "IDs out of order", seqs: []int64{3, 1, 2}, want: nil},
		{name: "nil", seqs: nil, want: stack.ErrJournalBatch},
		{name: "empty", seqs: []int64{}, want: stack.ErrJournalBatch},
		{name: "zero", seqs: []int64{0}, want: stack.ErrJournalBatch},
		{name: "negative", seqs: []int64{-5}, want: stack.ErrJournalBatch},
		{name: "zero after a valid ID", seqs: []int64{1, 0}, want: stack.ErrJournalBatch},
		{name: "repeated", seqs: []int64{2, 2}, want: stack.ErrJournalBatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := stack.CheckJournalSequenceIDs(tc.seqs)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("CheckJournalSequenceIDs(%v) = %v, want %v", tc.seqs, err, tc.want)
			}
		})
	}
}
