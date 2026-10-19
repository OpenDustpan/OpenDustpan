// Package refusal writes the audit entry of a request that the Authorizer
// refuses. The tool adapters check their routes through Recorder.Authorize, so
// a refusal writes its entry the same way for every tool.
package refusal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// channelHeader names the request header that tells the channel of a request.
const channelHeader = "Dustpan-Channel"

// errNoServerStore reports that the audit store cannot write the scope of the
// installation.
var errNoServerStore = errors.New("audit store has no installation scope")

// Namespaces reads a namespace to take its fence. The namespace service
// implements it.
type Namespaces interface {
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Recorder asks the Authorizer and writes one audit entry of the outcome
// denied for each refusal. A nil store writes no entry.
type Recorder struct {
	authz      identity.Authorizer
	store      audit.Store
	namespaces Namespaces
	logger     *slog.Logger
	onFailure  func()
}

// New returns a Recorder. OnFailure runs when an append fails. Nil means no
// counter.
func New(
	authz identity.Authorizer,
	store audit.Store,
	namespaces Namespaces,
	logger *slog.Logger,
	onFailure func(),
) *Recorder {
	return &Recorder{
		authz:      authz,
		store:      store,
		namespaces: namespaces,
		logger:     logger,
		onFailure:  onFailure,
	}
}

// Authorize asks the Authorizer whether p may do action on obj, and returns
// its answer. A refusal of an authenticated principal writes an entry before
// Authorize returns. The entry records the action and the object, never the
// request body. A failed append logs and counts the failure. It does not
// change the answer: the refusal is still returned, and a refusal never turns
// into a success.
func (r *Recorder) Authorize(
	ctx context.Context, req *http.Request, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	err := r.authz.Authorize(ctx, p, action, obj)
	if err == nil || !errors.Is(err, identity.ErrDenied) {
		return err
	}

	// A guest has no subject. The adapters authenticate a caller before they
	// check its routes, so a guest reaches here only through a bug, and it
	// writes nothing.
	if p.Subject == "" {
		return err
	}

	r.record(ctx, req, p, action, obj)

	return err
}

// record appends the entry of one refusal, and logs and counts a failure.
func (r *Recorder) record(
	ctx context.Context, req *http.Request, p identity.Principal, action identity.Action, obj identity.Object,
) {
	if r.store == nil {
		return
	}

	entry := deniedEntry(ctx, req, p, action, obj)

	if err := r.append(ctx, obj, entry); err != nil {
		r.logger.ErrorContext(ctx, "audit append failed for a refused request",
			slog.String("action", string(action)), slog.String("object", obj.String()),
			slog.String("error", err.Error()))

		if r.onFailure != nil {
			r.onFailure()
		}
	}
}

// append writes entry in the scope of obj: the namespace of obj, or the
// installation scope for an installation object.
func (r *Recorder) append(ctx context.Context, obj identity.Object, entry audit.Entry) error {
	if obj.IsServer() {
		ss, ok := r.store.(audit.ServerStore)
		if !ok {
			return errNoServerStore
		}

		if _, err := ss.AppendServer(ctx, entry); err != nil {
			return fmt.Errorf("append installation entry: %w", err)
		}

		return nil
	}

	ns, err := r.namespaces.Get(ctx, obj.Namespace())
	if err != nil {
		return fmt.Errorf("read namespace: %w", err)
	}

	if _, err := r.store.Append(ctx, ns.Fence(), entry); err != nil {
		return fmt.Errorf("append entry: %w", err)
	}

	return nil
}

// deniedEntry returns the entry of one refusal, with the outcome denied.
func deniedEntry(
	ctx context.Context, req *http.Request, p identity.Principal, action identity.Action, obj identity.Object,
) audit.Entry {
	scope := obj.Namespace()
	if obj.IsServer() {
		scope = audit.ServerScope
	}

	return audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     scope,
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(action),
		Object:        obj.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(req),
		UserAgent:     truncateUserAgent(req.UserAgent()),
		Channel:       audit.NormalizeChannel(req.Header.Get(channelHeader)),
		Kind:          kindOf(action),
		Outcome:       audit.OutcomeDenied,
		RequestID:     requestid.FromContext(ctx),
		ObjectID:      obj.UnitID(),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: string(p.Kind),
		JobID:         p.JobID,
	}
}

// kindOf returns the audit kind of the entry for the refused action.
func kindOf(action identity.Action) audit.Kind {
	switch {
	case action == identity.ActionUnitReadSecrets:
		return audit.KindReveal
	case action == identity.ActionUnitWriteForced, action == identity.ActionUnitWriteUnlocked:
		return audit.KindOverride
	case strings.HasSuffix(string(action), ":read"):
		return audit.KindToolOperation
	default:
		return audit.KindChange
	}
}

// truncateUserAgent cuts s to audit.MaxUserAgentLen bytes, at a rune boundary.
func truncateUserAgent(s string) string {
	if len(s) <= audit.MaxUserAgentLen {
		return s
	}

	cut := audit.MaxUserAgentLen
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}

	return s[:cut]
}
