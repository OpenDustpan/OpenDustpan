// Package unitsettings holds the settings of each unit: the code location, the
// executor, the runner pool, the write-back options and the custom steps that
// the job planner reads at the start of a job.
//
// One settings record belongs to one unit, by its stable unit ID. A namespace
// has defaults for the runner pool only. The default Git host connection
// belongs to the forge connections of the namespace. There is no merge by
// field across levels: a field of the record is set, or the next level gives
// it (Resolve). Each answer shows the effective value of each field and the
// level that gave it.
//
// This package holds the types, the checks, the effective value and the Store
// port. The SQL adapters live in pkg/store. The HTTP routes live in the
// inventory API.
package unitsettings

import (
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Limits of the settings. They bound the stored record.
const (
	// MaxFiles is the largest number of write-back files in one record.
	MaxFiles = 16
	// MaxFileLen is the largest length of the path of a write-back file, in
	// bytes.
	MaxFileLen = 255
	// MaxBodyBytes is the largest request body of a write, in bytes.
	MaxBodyBytes = 256 << 10
)

// AuditActions of the writes of this package. The entry of a write holds one
// change for each changed field.
const (
	// AuditActionChanged is the action of a change of the settings of a unit.
	AuditActionChanged = "unit-settings-changed"
	// AuditActionReset is the action of the removal of the settings of a unit.
	AuditActionReset = "unit-settings-reset"
	// AuditActionDefaultsChanged is the action of a change of the unit
	// defaults of a namespace.
	AuditActionDefaultsChanged = "unit-defaults-changed"
)

var (
	// ErrInvalid reports settings, a unit or defaults that fail validation.
	ErrInvalid = errors.New("unit settings are not valid")
	// ErrNotFound reports that a unit has no settings record. A unit with no
	// record is a valid unit: its fields come from the next level.
	ErrNotFound = errors.New("unit settings not found")
	// ErrUnknownUnit reports a unit that does not exist in its namespace, or
	// whose stable unit ID and name point to different units.
	ErrUnknownUnit = errors.New("unit not found")
	// ErrConflict reports that the stored version is not the version that
	// the caller read. A writer that gets it reads the record again.
	ErrConflict = errors.New("unit settings changed since the version that was read")
)

// Settings is the settings record of one unit. An empty field means that the
// field is not set at this level. Resolve gives the effective value.
type Settings struct {
	// Unit is the unit reference with its stable unit ID set
	// (identity.Object.WithUnitID). A rename of the unit keeps the record.
	Unit identity.Object `json:"-"`
	// Executor is empty, runner or write-back.
	Executor job.ExecutorKind `json:"executor,omitempty"`
	// Pool is the ID of the runner pool for jobs, or empty.
	Pool string `json:"pool,omitempty"`
	// CodeLocation is the one code location of the unit.
	CodeLocation CodeLocation `json:"codeLocation"`
	// WriteBack holds the options of the write-back executor.
	WriteBack WriteBack `json:"writeBack"`
	// ReviewRequests holds the settings of pull request automation.
	ReviewRequests ReviewRequests `json:"reviewRequests"`
	// Steps are the custom steps of the unit. No step means the default steps
	// of the tool. The server marks the steps with job.MarkCustomSteps at
	// the start of a job, so ChangesResources is false here.
	Steps []job.Step `json:"-"`
	// Version counts the writes of the record, from 1. It is 0 for a unit
	// with no record. A write carries the version that the caller read.
	Version int64 `json:"-"`
	// UpdatedAt is the time of the last write, or the zero time for a unit
	// with no record.
	UpdatedAt time.Time `json:"-"`
}

// CodeLocation is the place in a Git host where the code of a unit lives. A
// unit has one code location. A module of another repository is a module
// source of the code, which the git credential mode of the runner pool
// serves.
type CodeLocation struct {
	// Connection is the ID of the Git host connection, or empty for the
	// default connection of the namespace.
	Connection string `json:"connection,omitempty"`
	// Repository is the repository, such as acme/infra, or an https URL.
	Repository string `json:"repository,omitempty"`
	// Ref is the branch that the unit deploys from. It is the default branch
	// of a review request.
	Ref string `json:"ref,omitempty"`
	// Directory is the working directory: a clean relative path, or empty
	// for the root of the repository.
	Directory string `json:"directory,omitempty"`
}

// WriteBack holds the options of the write-back executor.
type WriteBack struct {
	// Delivery is empty, review-request or direct-push.
	Delivery job.Delivery `json:"delivery,omitempty"`
	// Files are the config files of the unit that a write-back can edit,
	// relative to the directory of the code location. No file means the
	// default file of the tool. The step planner of the tool checks each file
	// again with its Editable rule.
	Files []string `json:"files,omitempty"`
}

// ReviewRequests holds the settings of pull request automation.
type ReviewRequests struct {
	// Enabled turns the automation on. It is off by default.
	Enabled bool `json:"enabled,omitempty"`
}

// NamespaceDefaults are the defaults above the unit. Only the runner pool has
// one. The default Git host connection belongs to the forge connections.
type NamespaceDefaults struct {
	// Namespace is the namespace that the defaults belong to. It is set on
	// the defaults that a store returns, and a write names it here.
	Namespace namespace.Name
	// Pool is the ID of the default runner pool for jobs, or empty.
	Pool string
	// Version counts the writes of the defaults, from 1. It is 0 for a
	// namespace that never set them.
	Version int64
	// UpdatedAt is the time of the last write, or the zero time.
	UpdatedAt time.Time
}
