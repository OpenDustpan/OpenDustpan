package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// stateWriteGatesKey is the key of the effective value of the state.write
// gates in the start log and in the audit entry.
const stateWriteGatesKey = "state.write gates"

// stateWriteGatesPath is the value path of the audit entry of the gates off.
const stateWriteGatesPath = "/state-write-gates"

// stateWriteGatesSubject is the subject of the audit entry of the gates off:
// the server writes it at the start, and no principal acts.
const stateWriteGatesSubject = "server"

// errStateWriteNoAudit means that the gates are off and the server has no
// audit store of the installation scope to record it.
var errStateWriteNoAudit = errors.New("state.write gates are off and the server cannot record it in the audit log")

// stateWriteStart is what the start needs to record the effective value of
// the state.write gates.
type stateWriteStart struct {
	effective stategate.Effective
	// audit is the audit store of the installation scope. The option check
	// makes it required.
	audit audit.ServerStore
}

// newStateWriteStart returns the start record of the state.write gates of
// svc on the audit store of the installation scope of cfg. A store with no
// such audit store gives no audit store here: the option check refuses it,
// and the start fails when the gates are off.
func newStateWriteStart(cfg *settings, svc services) stateWriteStart {
	server, ok := cfg.namespaceStore.(audit.ServerStore)
	if !ok {
		server = nil
	}

	return stateWriteStart{effective: svc.stateWrite.effective(), audit: server}
}

// recordStateWriteGates writes the effective value of the state.write gates
// to the start log. When the gates are off, it also writes one audit entry in
// the installation scope with the reason, so that an operator can see that
// no write is checked and why. With the gates off and no audit store, or an
// append that fails, the start fails: the gates are never off with no
// record.
func (s *Server) recordStateWriteGates(ctx context.Context) error {
	eff := s.stateWrite.effective

	if eff.Mode != stategate.ModeOff {
		s.logger.InfoContext(ctx, "state.write gates: on", slog.String("reason", eff.Reason))

		return nil
	}

	s.logger.WarnContext(ctx, "state.write gates: off", slog.String("reason", eff.Reason))

	// Defense in depth: the option check refuses a store with no audit
	// entries of the installation scope. This check stays so that a server
	// built another way still does not run with the gates off and no
	// record. It does not rely on the option check.
	if s.stateWrite.audit == nil {
		return errStateWriteNoAudit
	}

	if _, err := s.stateWrite.audit.AppendServer(ctx, stateWriteOffEntry(eff, s.now())); err != nil {
		return fmt.Errorf("record state.write gates off: %w", err)
	}

	return nil
}

// stateWriteOffEntry returns the audit entry of the state.write gates off.
func stateWriteOffEntry(eff stategate.Effective, at time.Time) audit.Entry {
	return audit.Entry{
		At:         at.UTC(),
		Namespace:  audit.ServerScope,
		Subject:    stateWriteGatesSubject,
		IDPSubject: "",
		TokenID:    "",
		Action:     string(identity.ActionServerManageSettings),
		Object:     audit.ServerScope.String(),
		ObjectID:   "",
		Version:    "",
		ValuePath:  stateWriteGatesPath,
		ValueCount: nil,
		Changes: []audit.Change{
			{Key: stateWriteGatesKey, Old: nil, New: string(eff.Mode), Layer: "", TakesEffect: ""},
		},
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelStartup,
		Kind:          audit.KindSecurity,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        eff.Reason,
		PrincipalKind: "",
		JobID:         "",
	}
}
