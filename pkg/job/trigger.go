package job

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Bounds of a TriggerRef.
const (
	// MaxTriggerIDBytes is the longest trigger ID.
	MaxTriggerIDBytes = 64
	// MaxTriggerSaverBytes is the longest subject of a saver.
	MaxTriggerSaverBytes = 255
)

// TriggerRef names the run trigger that started a job, and its last saver at
// the time of the start. The package never reads the trigger: it keeps the
// reference, so that the audit entry of the start names both, and so that the
// read of an output can find the trigger.
type TriggerRef struct {
	// ID is the ID of the trigger. It has 1 to MaxTriggerIDBytes characters
	// from a-z, 0-9, "-" and "_".
	ID string
	// Saver is the subject of the last saver of the trigger at the start.
	Saver string
}

// Validate returns an error that wraps ErrInvalid when r is not valid.
// Defense in depth: the trigger package checks the same values, and this check
// stays so that a caller of the job service cannot put a value into the audit
// entry or the record of a job that the trigger rules refuse.
func (r *TriggerRef) Validate() error {
	err := r.validateID()
	if err != nil {
		return err
	}

	switch {
	case r.Saver == "" || len(r.Saver) > MaxTriggerSaverBytes:
		return fmt.Errorf("%w: the saver of a trigger has 1 to %d bytes", ErrInvalid, MaxTriggerSaverBytes)
	case !utf8.ValidString(r.Saver) || strings.ContainsFunc(r.Saver, audit.IsHiddenRune):
		return fmt.Errorf("%w: the saver of a trigger has a byte that is not allowed", ErrInvalid)
	case identity.IsNamespaceServiceSubject(r.Saver):
		return fmt.Errorf("%w: a namespace service principal cannot be the saver of a trigger", ErrInvalid)
	default:
		return nil
	}
}

// validateID checks the trigger ID of r.
func (r *TriggerRef) validateID() error {
	if r.ID == "" || len(r.ID) > MaxTriggerIDBytes {
		return fmt.Errorf("%w: a trigger ID has 1 to %d characters", ErrInvalid, MaxTriggerIDBytes)
	}

	for i := range len(r.ID) {
		c := r.ID[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return fmt.Errorf("%w: a trigger ID has a byte that is not in a-z, 0-9, \"-\" or \"_\"", ErrInvalid)
		}
	}

	return nil
}

// checkTrigger checks the trigger of a start request against the actor and the
// source kind. A start by the namespace service principal is the start of a
// trigger and nothing else, and a start that carries a trigger is a manual
// fire by a person (source api) or a start by the namespace service
// principal. So no person can pass for a trigger, and the principal never
// starts a job that no trigger owns.
func checkTrigger(a Actor, source SourceKind, ref *TriggerRef) error {
	service := a.Principal.IsNamespaceService()

	// Defense in depth: the principal API refuses this subject, and the
	// Authorizer denies it every action unless the credential is the server
	// credential. A name that looks like the principal but is not one gets no
	// start here either.
	if identity.IsNamespaceServiceSubject(a.Principal.Subject) && !service {
		return fmt.Errorf("%w: the subject is reserved for the namespace service principal", identity.ErrDenied)
	}

	if ref == nil {
		if service {
			return fmt.Errorf("%w: the namespace service principal starts jobs for triggers only", ErrInvalid)
		}

		return nil
	}

	err := ref.Validate()
	if err != nil {
		return err
	}

	if service {
		return checkServiceSource(source)
	}

	return checkManualSource(a, source)
}

// checkServiceSource checks the source kind of a start by the namespace
// service principal.
func checkServiceSource(source SourceKind) error {
	if source == SourceSchedule || source == SourceForge || source == SourceTrigger {
		return nil
	}

	return fmt.Errorf("%w: a start by a trigger has the source schedule, forge or trigger", ErrInvalid)
}

// checkManualSource checks the source kind and the agent of a manual fire.
func checkManualSource(a Actor, source SourceKind) error {
	switch {
	case source != SourceAPI:
		return fmt.Errorf("%w: a manual start by a trigger has the source api", ErrInvalid)
	case a.Agent != "":
		return fmt.Errorf("%w: an agent cannot fire a trigger", ErrAgentDenied)
	default:
		return nil
	}
}
