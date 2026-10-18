package gate

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/gate"
)

// Config stores gates, approval defaults, approval rules and owner rules.
// The server keeps them; no repository file does.
type Config interface {
	PutGate(g gate.Gate) error
	PutDefault(d gate.ApprovalDefault) error
	PutRule(r gate.ApprovalRule) error
	PutOwnerRule(o gate.OwnerRule) error
}

// Route patterns of the configuration routes.
const (
	GatePattern      = "PUT /spike/v1/gates/{id...}"
	DefaultPattern   = "PUT /spike/v1/approval-defaults/{id}"
	RulePattern      = "PUT /spike/v1/approval-rules/{id}"
	OwnerRulePattern = "PUT /spike/v1/owner-rules/{id}"
)

// maxBody is the largest body of a configuration route, in bytes.
const maxBody = 64 << 10

// AuthorizeConfig decides whether the request may change the configuration
// of the scope. It returns false for a refusal.
type AuthorizeConfig func(r *http.Request, scope gate.Scope) bool

// Register adds the configuration routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, cfg Config, allow AuthorizeConfig) {
	mux.Handle(GatePattern, put(log, allow, func(g gate.Gate) error {
		return cfg.PutGate(g)
	}))
	mux.Handle(DefaultPattern, put(log, allow, func(d gate.ApprovalDefault) error {
		return cfg.PutDefault(d)
	}))
	mux.Handle(RulePattern, put(log, allow, func(r gate.ApprovalRule) error {
		return cfg.PutRule(r)
	}))
	mux.Handle(OwnerRulePattern, put(log, allow, func(o gate.OwnerRule) error {
		return cfg.PutOwnerRule(o)
	}))
}

// put returns a handler that decodes a body, checks the scope of the body
// with allow, checks that the ID in the path is the ID in the body, and
// stores the object.
func put[T any](log *slog.Logger, allow AuthorizeConfig, store func(T) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v T

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
		dec.DisallowUnknownFields()

		err := dec.Decode(&v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Body is not valid JSON of the object.")

			return
		}

		scope, id := probe(v)
		if !allow(r, scope) {
			writeError(w, http.StatusForbidden, "Caller may not change the configuration of this scope.")

			return
		}

		if id != r.PathValue("id") {
			writeError(w, http.StatusBadRequest, "ID in the path is not the ID in the body.")

			return
		}

		err = store(v)

		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, gate.ErrFinal):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, gate.ErrInvalid):
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		default:
			log.ErrorContext(r.Context(), "store configuration", slog.Any("error", err))
			writeError(w, http.StatusInternalServerError, "Configuration could not be stored.")
		}
	})
}

func probe(v any) (gate.Scope, string) {
	switch t := v.(type) {
	case gate.Gate:
		return t.Scope, t.ID
	case gate.ApprovalDefault:
		return t.Scope, t.ID
	case gate.ApprovalRule:
		return gate.Scope{Level: gate.LevelInstallation}, t.Name
	case gate.OwnerRule:
		return t.Scope, t.ID
	default:
		return gate.Scope{}, ""
	}
}
