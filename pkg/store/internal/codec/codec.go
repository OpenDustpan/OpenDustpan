// Package codec converts between domain types and the column encodings shared
// by the SQL store adapters: timestamps as Unix milliseconds.
package codec

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
)

// NamespaceRow is the column representation of a namespace.
type NamespaceRow struct {
	Name                string
	HomeRegion          string
	FailoverVersion     int64
	CreatedAtMs         int64
	AllowForcedWrites   bool
	AllowUnlockedWrites bool
}

// StackRow is the column representation of a stack. Tags live in the
// stack_tags table, keyed by the stack ID, so they are not columns here.
type StackRow struct {
	Namespace   string
	Project     string
	Name        string
	ID          string
	Version     int64
	CreatedAtMs int64
	UpdatedAtMs int64
}

// Millis encodes t as Unix milliseconds.
func Millis(t time.Time) int64 {
	return t.UnixMilli()
}

// FromMillis decodes Unix milliseconds as a UTC time.
func FromMillis(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

// FromStackRow converts a row and its tags into a domain stack. A nil tags
// map becomes an empty one, so callers always see an object and never null.
func FromStackRow(row StackRow, tags map[string]string) stack.Stack {
	if tags == nil {
		tags = map[string]string{}
	}

	return stack.Stack{
		CreatedAt: FromMillis(row.CreatedAtMs),
		UpdatedAt: FromMillis(row.UpdatedAtMs),
		Tags:      tags,
		Ref: stack.Ref{
			Namespace: namespace.Name(row.Namespace),
			Project:   stack.ProjectName(row.Project),
			Stack:     stack.Name(row.Name),
		},
		ID:      stack.ID(row.ID),
		Version: row.Version,
	}
}

// ToStackRow converts a domain stack into a row. Tags go to the stack_tags
// table, so they are not part of the row.
func ToStackRow(st stack.Stack) StackRow {
	return StackRow{
		Namespace:   st.Ref.Namespace.String(),
		Project:     st.Ref.Project.String(),
		Name:        st.Ref.Stack.String(),
		ID:          st.ID.String(),
		Version:     st.Version,
		CreatedAtMs: Millis(st.CreatedAt),
		UpdatedAtMs: Millis(st.UpdatedAt),
	}
}

// FromNamespaceRow converts a row into a domain namespace.
func FromNamespaceRow(row NamespaceRow) namespace.Namespace {
	return namespace.Namespace{
		CreatedAt:       FromMillis(row.CreatedAtMs),
		Name:            namespace.Name(row.Name),
		HomeRegion:      namespace.Region(row.HomeRegion),
		Overrides:       namespace.Overrides{Forced: row.AllowForcedWrites, Unlocked: row.AllowUnlockedWrites},
		FailoverVersion: row.FailoverVersion,
	}
}

// ToNamespaceRow converts a domain namespace into a row.
func ToNamespaceRow(ns namespace.Namespace) NamespaceRow {
	return NamespaceRow{
		Name:                ns.Name.String(),
		HomeRegion:          ns.HomeRegion.String(),
		FailoverVersion:     ns.FailoverVersion,
		CreatedAtMs:         Millis(ns.CreatedAt),
		AllowForcedWrites:   ns.Overrides.Forced,
		AllowUnlockedWrites: ns.Overrides.Unlocked,
	}
}

// SecretKeyRow is the column representation of a wrapped data key, keyed by
// namespace and stack ID.
type SecretKeyRow struct {
	Namespace   string
	StackID     string
	WrapperID   string
	Ciphertext  []byte
	CreatedAtMs int64
}

// ToSecretKeyRow converts a domain wrapped key into a row.
func ToSecretKeyRow(k secret.WrappedKey) SecretKeyRow {
	return SecretKeyRow{
		Namespace:   k.Scope.Namespace.String(),
		StackID:     k.Scope.ID,
		WrapperID:   k.WrapperID,
		Ciphertext:  k.Ciphertext,
		CreatedAtMs: Millis(k.CreatedAt),
	}
}

// FromSecretKeyRow converts a row into a domain wrapped key.
func FromSecretKeyRow(row SecretKeyRow) secret.WrappedKey {
	return secret.WrappedKey{
		CreatedAt: FromMillis(row.CreatedAtMs),
		Scope: secret.Scope{
			Namespace: namespace.Name(row.Namespace),
			ID:        row.StackID,
		},
		WrapperID:  row.WrapperID,
		Ciphertext: row.Ciphertext,
	}
}

// BackupRow is the column representation of a backup registry row. The end
// time is 0 while the backup runs.
type BackupRow struct {
	ID          string
	State       string
	Target      string
	Error       string
	StartedAtMs int64
	EndedAtMs   int64
}

// ToBackupRow converts a domain backup record into a row.
func ToBackupRow(r backup.Record) BackupRow {
	return BackupRow{
		ID:          r.ID.String(),
		State:       string(r.State),
		Target:      r.Target,
		Error:       r.Error,
		StartedAtMs: Millis(r.StartedAt),
		EndedAtMs:   OptionalMillis(r.EndedAt),
	}
}

// FromBackupRow converts a row into a domain backup record.
func FromBackupRow(row BackupRow) (backup.Record, error) {
	switch state := backup.State(row.State); state {
	case backup.StateRunning, backup.StateSucceeded, backup.StateFailed:
		return backup.Record{
			StartedAt: FromMillis(row.StartedAtMs),
			EndedAt:   FromOptionalMillis(row.EndedAtMs),
			ID:        backup.ID(row.ID),
			Target:    row.Target,
			Error:     row.Error,
			State:     state,
		}, nil
	default:
		return backup.Record{}, fmt.Errorf("%w: unknown backup state %q", ErrCorruptRow, row.State)
	}
}

// RestoreRow is the column representation of a restore registry row. The end
// time is 0 while the restore runs. PreBackupID stays empty when the restore
// stopped before the apply.
type RestoreRow struct {
	ID          string
	State       string
	BackupID    string
	PreBackupID string
	RequestedBy string
	Error       string
	StartedAtMs int64
	EndedAtMs   int64
}

// ToRestoreRow converts a domain restore record into a row.
func ToRestoreRow(r backup.RestoreRecord) RestoreRow {
	return RestoreRow{
		ID:          r.ID.String(),
		State:       string(r.State),
		BackupID:    r.BackupID.String(),
		PreBackupID: r.PreBackupID.String(),
		RequestedBy: r.RequestedBy,
		Error:       r.Error,
		StartedAtMs: Millis(r.StartedAt),
		EndedAtMs:   OptionalMillis(r.EndedAt),
	}
}

// FromRestoreRow converts a row into a domain restore record.
func FromRestoreRow(row RestoreRow) (backup.RestoreRecord, error) {
	switch state := backup.RestoreState(row.State); state {
	case backup.RestoreChecking, backup.RestoreApplying, backup.RestoreSucceeded, backup.RestoreFailed:
		return backup.RestoreRecord{
			StartedAt:   FromMillis(row.StartedAtMs),
			EndedAt:     FromOptionalMillis(row.EndedAtMs),
			ID:          backup.RestoreID(row.ID),
			BackupID:    backup.ID(row.BackupID),
			PreBackupID: backup.ID(row.PreBackupID),
			RequestedBy: row.RequestedBy,
			Error:       row.Error,
			State:       state,
		}, nil
	default:
		return backup.RestoreRecord{}, fmt.Errorf("%w: unknown restore state %q", ErrCorruptRow, row.State)
	}
}
