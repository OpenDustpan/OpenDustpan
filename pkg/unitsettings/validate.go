package unitsettings

import (
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

// placeholderRepository and placeholderRef stand in for an unset field, so
// that job.Location.Validate, the one rule of a code location, checks the
// other fields of a partial location.
const (
	placeholderRepository = "acme/infra"
	placeholderRef        = "main"
	maxPoolLen            = 64
)

// Validate returns an error that wraps ErrInvalid for settings that the
// server must not store. It checks:
//   - the unit: a unit reference with a stable unit ID,
//   - the executor and the delivery: empty or a known value,
//   - the pool: empty or a runner pool ID,
//   - the connection: empty or a Git host connection ID,
//   - the repository, the ref and the directory with the rule of
//     job.Location.Validate, and the directory and each file as a path of a
//     write-back (no path outside the directory, no CI configuration),
//   - at most MaxFiles files, each unique, and
//   - the steps with job.ValidateSteps, with ChangesResources false.
//
// Validate refuses a value and never changes it. The error never holds the
// value of a field.
//
// Defense in depth: the route checks the same fields at the boundary, and
// the job planner and the runner check the location again. This check stays
// so that a new caller of the Store cannot save what another layer would
// refuse.
func (s *Settings) Validate() error {
	if !s.Unit.IsUnit() || s.Unit.UnitID() == "" {
		return fmt.Errorf("%w: the unit must be a unit reference with a stable unit ID", ErrInvalid)
	}

	return s.validateFields()
}

// validateFields checks every field except the unit. Parse uses it for the
// body of a stored record.
func (s *Settings) validateFields() error {
	if s.Executor != "" && !s.Executor.Valid() {
		return fmt.Errorf("%w: the executor must be %q or %q", ErrInvalid, job.ExecutorRunner,
			job.ExecutorWriteBack)
	}

	if err := CheckPool(s.Pool); err != nil {
		return err
	}

	if err := s.CodeLocation.validate(); err != nil {
		return err
	}

	if err := s.WriteBack.validate(s.CodeLocation.Directory); err != nil {
		return err
	}

	return validateSteps(s.Steps)
}

// CheckPool returns an error that wraps ErrInvalid for a pool that is neither
// empty nor a runner pool ID.
func CheckPool(pool string) error {
	if pool == "" {
		return nil
	}

	id := strings.TrimPrefix(pool, runner.PoolIDPrefix)
	if id == pool || id == "" || len(pool) > maxPoolLen || strings.ContainsFunc(id, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'z') && r != '-'
	}) {
		return fmt.Errorf("%w: the pool must be the ID of a runner pool", ErrInvalid)
	}

	return nil
}

// CheckConnection returns an error that wraps ErrInvalid for a connection that
// is neither empty nor a Git host connection ID.
func CheckConnection(connection string) error {
	if connection != "" && !forge.ValidConnectionID(connection) {
		return fmt.Errorf("%w: the connection must be the ID of a Git host connection", ErrInvalid)
	}

	return nil
}

// validate checks the code location.
func (l CodeLocation) validate() error {
	if err := CheckConnection(l.Connection); err != nil {
		return err
	}

	// The directory "." is refused: an empty directory is the root, so the
	// root has one spelling.
	loc := job.Location{
		Connection: "", Repository: orDefault(l.Repository, placeholderRepository),
		Ref: orDefault(l.Ref, placeholderRef), Directory: l.Directory, Revision: "",
	}
	if err := loc.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

// validate checks the options of the write-back. dir is the directory of the
// code location.
func (w WriteBack) validate(dir string) error {
	switch w.Delivery {
	case "", job.DeliveryReviewRequest, job.DeliveryDirectPush:
	default:
		return fmt.Errorf("%w: the delivery must be %q or %q", ErrInvalid, job.DeliveryReviewRequest,
			job.DeliveryDirectPush)
	}

	if len(w.Files) > MaxFiles {
		return fmt.Errorf("%w: at most %d write-back files, got %d", ErrInvalid, MaxFiles, len(w.Files))
	}

	seen := make(map[string]bool, len(w.Files))

	for i, f := range w.Files {
		if len(f) > MaxFileLen {
			return fmt.Errorf("%w: write-back file %d has more than %d bytes", ErrInvalid, i, MaxFileLen)
		}

		if seen[f] {
			return fmt.Errorf("%w: write-back file %d is in the list twice", ErrInvalid, i)
		}

		seen[f] = true

		// job.ValidateEdits is the one rule of a path that a write-back may
		// edit. The probe key makes the edit complete. Every path passes the
		// editable rule here: the step planner of the tool applies its own.
		probe := job.FileEdit{Path: f, Keys: map[string][]byte{"probe": []byte("1")}}
		if err := job.ValidateEdits(dir, []job.FileEdit{probe}, func(string) bool { return true }); err != nil {
			return fmt.Errorf("%w: write-back file %d: not a path that a write-back can edit", ErrInvalid, i)
		}
	}

	return nil
}

// validateSteps checks the custom steps. No step is valid: the tool gives its
// default steps.
func validateSteps(steps []job.Step) error {
	if len(steps) == 0 {
		return nil
	}

	for i := range steps {
		if steps[i].ChangesResources {
			return fmt.Errorf("%w: step %d sets changesResources: the server sets it", ErrInvalid, i)
		}
	}

	if err := job.ValidateSteps(steps); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

// Validate returns an error that wraps ErrInvalid for defaults that the server
// must not store.
func (d NamespaceDefaults) Validate() error {
	return CheckPool(d.Pool)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}
