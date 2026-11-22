package profile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/profile"
)

// openBody is the body of an open: the paths to answer, the consumer that
// reads them, and the life of the session. Empty paths mean all paths, and
// an empty duration means the default of two hours.
type openBody struct {
	Paths    []string `json:"paths,omitempty"`
	Consumer string   `json:"consumer"`
	Duration string   `json:"duration,omitempty"`
}

// openResponse is the answer of an open: the session ID and its end. It
// never holds a value.
type openResponse struct {
	SessionID string `json:"sessionId"` //nolint:tagliatelle // The apidesc camelCase rule spells an ID as Id; sessionId is the wire name.
	ExpiresAt string `json:"expiresAt"`
}

// sessionValue is one value of a session read.
type sessionValue struct {
	Value  jsonValue `json:"value"`
	Secret bool      `json:"secret,omitempty"`
}

// sessionResponse is the answer of a session read: the plain values by
// path.
type sessionResponse struct {
	Values map[string]sessionValue `json:"values"`
}

// open serves POST /profiles/{profile}/open. It checks profile:open on the
// namespace, opens the profile, and answers the session ID and its end. The
// route is keyed: a replay answers 409 with the session ID and no value.
func (h *Handler) open(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	target, err := targetOf(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var body openBody
	if decodeErr := route.DecodeBody(w, r, maxBodyBytes, &body); decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	// Defense in depth: the service checks the paths, the consumer and the
	// duration too; these checks stay so that a hostile body never reaches
	// the service, and so that a new caller of the service cannot skip them.
	if checkErr := checkOpenBody(body); checkErr != nil {
		h.writeError(ctx, w, checkErr)

		return
	}

	duration, err := parseOpenDuration(body.Duration)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	opened, err := h.opens.Open(ctx, actorOf(r, p), profile.OpenInput{
		Namespace: obj.Namespace(), Ref: target.Ref, Paths: body.Paths, Consumer: profile.OpenConsumer(body.Consumer),
		Duration: duration,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	answer, err := encodeOpenAnswer(opened.SessionID, opened.ExpiresAt)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	idempotent.Write(w, answer)
}

// checkOpenBody checks the paths and the consumer of an open body with the
// rules of the service: at most MaxSessionPaths paths, each with the check of
// profile.CheckOpenPath, and a consumer from the allowlist.
func checkOpenBody(body openBody) error {
	if len(body.Paths) > profile.MaxSessionPaths {
		return fmt.Errorf("%w: an open asks at most %d paths", profile.ErrInvalid, profile.MaxSessionPaths)
	}

	for _, p := range body.Paths {
		if err := profile.CheckOpenPath(p); err != nil {
			return fmt.Errorf("open path: %w", err)
		}
	}

	if _, err := profile.ParseOpenConsumer(body.Consumer); err != nil {
		return fmt.Errorf("open consumer: %w", err)
	}

	return nil
}

// parseOpenDuration parses the duration of an open. Empty means the default.
// A duration outside the window of a session is refused.
func parseOpenDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: duration %q is not a duration: %w", profile.ErrInvalid, raw, err)
	}

	if d < profile.MinSessionDuration || d > profile.MaxSessionDuration {
		return 0, fmt.Errorf("%w: duration %s is outside %s to %s",
			profile.ErrInvalid, d, profile.MinSessionDuration, profile.MaxSessionDuration)
	}

	return d, nil
}

// encodeOpenAnswer makes the answer of an open: 201 with the session ID and
// its end.
func encodeOpenAnswer(id string, expiresAt time.Time) (idempotency.Answer, error) {
	var buf strings.Builder

	resp := openResponse{SessionID: id, ExpiresAt: formatTime(expiresAt)}

	text, err := json.Marshal(resp)
	if err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode open: %w", err)
	}

	buf.Write(text)

	return idempotency.Answer{
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(buf.String()),
		Status:  http.StatusCreated,
	}, nil
}

// openAnswer makes the answer that the replay record keeps from the result
// of the write: a profile.SessionWritten.
func openAnswer(obj identity.Object) answerFunc {
	_ = obj

	return func(result any) (idempotency.Answer, error) {
		w, ok := result.(profile.SessionWritten)
		if !ok {
			return idempotency.Answer{}, errWrittenResult
		}

		return encodeOpenAnswer(w.Session.ID(), w.Session.ExpiresAt())
	}
}

// openSecretID answers the session ID that the replay record keeps in place
// of the answer: a replay names the session and holds no value.
func openSecretID(result any) string {
	w, ok := result.(profile.SessionWritten)
	if !ok {
		return ""
	}

	return w.Session.ID()
}

// readSession serves GET /profile-sessions/{session}. It checks profile:open
// again and answers the values of the session, or one path. The answer is
// never cached: it can hold secrets.
func (h *Handler) readSession(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	if idempotent.Present(r) {
		h.writeError(ctx, w, errIdempotencyKey)

		return
	}

	id := r.PathValue("session")
	// Defense in depth: the service and the store reject a bad session ID
	// too; this check stays so that a forged ID never leaves the handler.
	if _, idErr := profile.ParseSessionID(id); idErr != nil {
		h.writeError(ctx, w, profileSessionNotFound(id))

		return
	}

	var paths []string

	if raw := queryOf(r).Get("path"); raw != "" {
		// Defense in depth: the service checks the path too; this check stays
		// so that a hostile path never reaches it.
		if pathErr := profile.CheckOpenPath(raw); pathErr != nil {
			h.writeError(ctx, w, fmt.Errorf("session path: %w", pathErr))

			return
		}

		paths = []string{raw}
	}

	data, err := h.opens.ReadSession(ctx, actorOf(r, p), obj.Namespace(), id, paths)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	resp := sessionResponse{Values: make(map[string]sessionValue, len(data.Values))}
	for path, value := range data.Values {
		resp.Values[path] = sessionValue{Value: value, Secret: data.Secrets[path]}
	}

	w.Header().Set("Cache-Control", noStore)
	h.writeJSON(ctx, w, http.StatusOK, resp)
}

// profileSessionNotFound answers ErrNotFound for a session ID that has no
// pos- prefix or holds another character.
func profileSessionNotFound(id string) error {
	return fmt.Errorf("open session %q: %w", id, profile.ErrNotFound)
}
