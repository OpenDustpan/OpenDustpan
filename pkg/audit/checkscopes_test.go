package audit_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errCheckRead is the read failure of checkStore.
var errCheckRead = errors.New("check read fails")

// checkStore is an audit.Store whose Check answers a fixed result for each
// scope. A scope in failing answers errCheckRead. Append and List are not
// used.
type checkStore struct {
	invalid map[namespace.Name]bool
	failing map[namespace.Name]bool
}

func (s checkStore) Append(context.Context, namespace.Fence, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errCheckRead //nolint:exhaustruct // Not used.
}

func (s checkStore) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	return audit.Listing{}, errCheckRead //nolint:exhaustruct // Not used.
}

func (s checkStore) Check(_ context.Context, ns namespace.Name) (audit.CheckResult, error) {
	if s.failing[ns] {
		return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: false}, errCheckRead
	}

	if s.invalid[ns] {
		return audit.CheckResult{Checked: 1, BadSequence: 1, Unchained: 0, Valid: false}, nil
	}

	return audit.CheckResult{Checked: 1, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// eachStore is a checkStore that also checks many scopes in one call. A
// set fail makes the call answer errCheckRead.
type eachStore struct {
	checkStore

	fail bool
}

func (s eachStore) CheckEach(ctx context.Context, scopes []namespace.Name) ([]audit.CheckResult, error) {
	if s.fail {
		return nil, errCheckRead
	}

	results := make([]audit.CheckResult, 0, len(scopes))

	for _, scope := range scopes {
		result, err := s.Check(ctx, scope)
		if err != nil {
			return nil, err
		}

		results = append(results, result)
	}

	return results, nil
}

// TestCheckScopesCountsOnlyMismatches proves that CheckScopes counts a
// scope as invalid only when its chain does not match. A failed read
// answers an error and counts no scope.
func TestCheckScopesCountsOnlyMismatches(t *testing.T) {
	t.Parallel()

	scopes := []namespace.Name{"acme", "beta", "gamma"}

	tests := []struct {
		name        string
		store       audit.Store
		wantInvalid int
		wantErr     bool
	}{
		{
			name:        "one scope does not match",
			store:       checkStore{invalid: map[namespace.Name]bool{"beta": true}, failing: nil},
			wantInvalid: 1,
			wantErr:     false,
		},
		{
			name:        "the read of one scope fails",
			store:       checkStore{invalid: nil, failing: map[namespace.Name]bool{"beta": true}},
			wantInvalid: 0,
			wantErr:     true,
		},
		{
			name: "one read fails and another scope does not match",
			store: checkStore{
				invalid: map[namespace.Name]bool{"gamma": true},
				failing: map[namespace.Name]bool{"acme": true},
			},
			wantInvalid: 1,
			wantErr:     true,
		},
		{
			name:        "the one read of all scopes fails",
			store:       eachStore{checkStore: checkStore{invalid: nil, failing: nil}, fail: true},
			wantInvalid: 0,
			wantErr:     true,
		},
		{
			name: "one read of all scopes finds a mismatch",
			store: eachStore{
				checkStore: checkStore{invalid: map[namespace.Name]bool{"acme": true}, failing: nil},
				fail:       false,
			},
			wantInvalid: 1,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			invalid, err := audit.CheckScopes(t.Context(), tt.store, scopes)
			if invalid != tt.wantInvalid {
				t.Errorf("invalid scopes = %d, want %d", invalid, tt.wantInvalid)
			}

			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Errorf("error = %v, want an error: %t", err, tt.wantErr)
			}

			if err != nil && !errors.Is(err, errCheckRead) {
				t.Errorf("error = %v, want errCheckRead", err)
			}
		})
	}
}
