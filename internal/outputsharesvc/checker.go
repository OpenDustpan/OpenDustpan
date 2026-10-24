package outputsharesvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// denialWriteTimeout bounds the append of the audit entry of a denial. The
// append runs on a context that the request cannot cancel, so a client that
// disconnects does not lose the entry.
const denialWriteTimeout = 5 * time.Second

// denialReasonPrefix starts the reason of the audit entry of a denial. The
// name of the check follows it.
const denialReasonPrefix = "read of outputs denied by the check "

// NamespaceReader reads a namespace by its name. It returns an error that wraps
// namespace.ErrNotFound for an unknown namespace.
type NamespaceReader interface {
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// CheckerConfig holds the dependencies of a Checker.
type CheckerConfig struct {
	// Authorizer decides the actions of a principal on the producer.
	Authorizer Authorizer
	// States tells the latest state of the producer.
	States outputshare.States
	// Namespaces gives the fence of the producer namespace for the audit
	// entry of a denial. *namespace.Service satisfies it.
	Namespaces NamespaceReader
	// Audit keeps the entry of each denial.
	Audit audit.Store
	// Logger reports an entry that the checker could not write.
	Logger *slog.Logger
	// Now is the clock of the audit entries. Nil means time.Now.
	Now func() time.Time
}

// Checker decides the read of the outputs of a unit by a principal. It
// implements outputshare.Checker for a read with the token or the session of a
// principal. The read by a job of another unit and the open of the profile
// block have no path here yet, so the checker denies them: it fails closed.
type Checker struct {
	authorizer Authorizer
	states     outputshare.States
	namespaces NamespaceReader
	audit      audit.Store
	logger     *slog.Logger
	now        func() time.Time
}

var _ outputshare.Checker = (*Checker)(nil)

// NewChecker returns a Checker. It returns ErrMissingDependency when a port is
// missing.
func NewChecker(cfg CheckerConfig) (*Checker, error) {
	if cfg.Authorizer == nil || cfg.States == nil || cfg.Namespaces == nil || cfg.Audit == nil || cfg.Logger == nil {
		return nil, fmt.Errorf("%w: authorizer, states, namespace store, audit store and logger", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Checker{
		authorizer: cfg.Authorizer, states: cfg.States, namespaces: cfg.Namespaces, audit: cfg.Audit,
		logger: cfg.Logger, now: now,
	}, nil
}

// probe is what the checker learns from its ports for one read. The checker
// reads all of it before it decides, so a denial by an early check takes the
// same calls as an allowed read.
type probe struct {
	state    outputshare.State
	stateErr error
	// outputsErr, secretsErr and readErr are the answers of the Authorizer for
	// unit:read-outputs, unit:read-secrets and unit:read on the producer.
	outputsErr error
	secretsErr error
	readErr    error
}

// CheckRead implements outputshare.Checker for a read by a principal. The
// principal needs unit:read-outputs on the producer, and unit:read-secrets for
// a request with Secrets. A grant of unit:read alone does not allow the read.
// The read must be of the latest state. A principal that does not hold
// unit:read on the producer reads outputs only, so a state that the client
// encrypted is refused with ErrClientEncrypted: the server cannot reduce it.
//
// Every denial is a *outputshare.DeniedError that unwraps to one sentinel, and
// it writes one audit entry in the namespace of the producer. A request that
// is not valid returns an error that wraps outputshare.ErrInvalid.
//
//nolint:gocritic // The port passes the request by value.
func (c *Checker) CheckRead(ctx context.Context, req outputshare.ReadRequest) (outputshare.Decision, error) {
	if err := validateRead(&req); err != nil {
		return outputshare.Decision{}, err
	}

	p := c.probe(ctx, req.Caller, req.Producer.Unit)

	if denied := c.decide(&req, &p); denied != nil {
		c.recordDenial(ctx, req.Caller, denied)

		return outputshare.Decision{}, denied
	}

	return outputshare.Decision{
		Kind:    outputshare.ReadByPrincipal,
		Rule:    "",
		Secrets: p.secretsErr == nil,
	}, nil
}

// validateRead checks the shape of the request. Defense in depth: the tool
// adapter builds the producer from its route, and this check stays so that a
// new caller cannot skip it.
func validateRead(req *outputshare.ReadRequest) error {
	if !req.Producer.Unit.IsUnit() {
		return fmt.Errorf("%w: the producer %q is not a unit", outputshare.ErrInvalid, req.Producer.Unit)
	}

	if req.Producer.Unit.UnitID() != "" {
		if err := req.Producer.Validate(); err != nil {
			return fmt.Errorf("producer of the read: %w", err)
		}
	}

	if !req.Attachment.IsZero() {
		return fmt.Errorf(
			"%w: a read by a principal names no attachment, it uses the grants of the caller", outputshare.ErrInvalid,
		)
	}

	return nil
}

// probe runs each port call of the read.
func (c *Checker) probe(ctx context.Context, caller identity.Principal, producer identity.Object) probe {
	state, stateErr := c.states.Latest(ctx, producer)

	return probe{
		state:      state,
		stateErr:   stateErr,
		outputsErr: c.authorizer.Authorize(ctx, caller, identity.ActionUnitReadOutputs, producer),
		secretsErr: c.authorizer.Authorize(ctx, caller, identity.ActionUnitReadSecrets, producer),
		readErr:    c.authorizer.Authorize(ctx, caller, identity.ActionUnitRead, producer),
	}
}

// decide returns the first denial in the order of the checks, or nil.
func (c *Checker) decide(req *outputshare.ReadRequest, p *probe) *outputshare.DeniedError {
	check, cause := firstFailure(req, p)
	if check == "" {
		return nil
	}

	return &outputshare.DeniedError{
		Err: cause, Check: check, Producer: req.Producer.Unit, Consumer: identity.Object{}, Hidden: false,
	}
}

// firstFailure returns the check that fails first, in the order of the checks,
// and its cause. It returns an empty check when every check passes.
func firstFailure(req *outputshare.ReadRequest, p *probe) (string, error) {
	if check, cause := failedIdentity(req, p); check != "" {
		return check, cause
	}

	return failedState(req, p)
}

// failedIdentity runs the checks of the caller, the producer, the grant and the
// secret output.
func failedIdentity(req *outputshare.ReadRequest, p *probe) (string, error) {
	switch {
	case req.Caller.Subject == "":
		return outputshare.CheckCaller, errNoCaller
	case req.Caller.JobTokenID() != "":
		return outputshare.CheckCaller, errJobPathMissing
	case errors.Is(p.stateErr, outputshare.ErrUnknownProducer):
		return outputshare.CheckProducer, p.stateErr
	case p.outputsErr != nil:
		return outputshare.CheckGrant, p.outputsErr
	case req.Secrets && p.secretsErr != nil:
		return outputshare.CheckSecrets, p.secretsErr
	default:
		return "", nil
	}
}

// failedState runs the checks of the latest state and of the state that the
// client encrypted.
func failedState(req *outputshare.ReadRequest, p *probe) (string, error) {
	switch {
	case p.stateErr != nil:
		return outputshare.CheckLatest, p.stateErr
	case p.state.ID == "" || req.Latest != p.state.ID:
		return outputshare.CheckLatest, errStaleState
	// Defense in depth: the route of a whole state checks unit:read itself,
	// and the writer of the state never stores outputs of an encrypted state.
	// This check stays so that a new route cannot answer outputs only from a
	// state that the server cannot reduce.
	case p.state.ClientEncrypted && p.readErr != nil:
		return outputshare.CheckClientEncrypted, errClientEncrypted
	default:
		return "", nil
	}
}

// Causes of a denial. They stay in DeniedError.Err for the audit entry and
// never reach a caller.
var (
	errNoCaller        = errors.New("caller has no subject")
	errJobPathMissing  = errors.New("read by a job token is not served yet")
	errStaleState      = errors.New("request is not of the latest state")
	errClientEncrypted = errors.New("latest state is encrypted by the client")
)

// recordDenial writes the audit entry of a denial in the namespace of the
// producer. It never changes the answer: a failed append is logged and the
// denial stands. A producer namespace that does not exist, and a caller with
// no subject, get no entry.
func (c *Checker) recordDenial(ctx context.Context, caller identity.Principal, denied *outputshare.DeniedError) {
	if caller.Subject == "" {
		return
	}

	// The write outlives the request: a canceled request must not lose the
	// entry of the denial.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), denialWriteTimeout)
	defer cancel()

	ns, err := c.namespaces.Get(ctx, denied.Producer.Namespace())
	if errors.Is(err, namespace.ErrNotFound) {
		return
	}

	if err != nil {
		c.logger.WarnContext(ctx, "read namespace for the denial of an output read failed",
			slog.String("check", denied.Check))

		return
	}

	entry := audit.Entry{
		At: c.now().UTC(), Namespace: ns.Name, Subject: caller.Subject, IDPSubject: "", TokenID: caller.TokenID,
		Action: string(identity.ActionUnitReadOutputs), Object: denied.Producer.String(), Version: "",
		ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.NormalizeChannel(""), Kind: audit.KindToolOperation, Outcome: audit.OutcomeDenied,
		RequestID: "", ObjectID: denied.Producer.UnitID(), PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		PrincipalKind: string(caller.Kind), JobID: caller.JobID, Reason: denialReasonPrefix + denied.Check,
	}

	if _, err := c.audit.Append(ctx, ns.Fence(), entry); err != nil {
		c.logger.WarnContext(ctx, "audit entry of the denial of an output read failed",
			slog.String("check", denied.Check))
	}
}
