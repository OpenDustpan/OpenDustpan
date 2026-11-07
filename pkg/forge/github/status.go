package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

// maxStatusAnswerBytes is the largest answer to a status that the adapter
// reads. The answer of GitHub has a few hundred bytes.
const maxStatusAnswerBytes = 64 << 10

// statusAllowed returns forge.ErrUnsupported for a connection that
// Capabilities does not list forge.CapStatus for.
func (f *gitHub) statusAllowed() error {
	if !f.Capabilities().Has(forge.CapStatus) {
		return fmt.Errorf(
			"%w: GitHub SetStatus needs a connection with push access and, for an app, the permission to write "+
				"the commit statuses",
			forge.ErrUnsupported,
		)
	}

	return nil
}

// Fields of the status call.
const (
	keyContext     = "context"
	keyDescription = "description"
	keyTargetURL   = "target_url"
)

// statusBody returns the body of the call that sets a status. It has no
// address when the status has none.
func statusBody(s forge.Status) ([]byte, error) {
	fields := map[string]string{
		keyState: string(s.State), keyContext: s.Context, keyDescription: s.Description,
	}

	if s.TargetURL != "" {
		fields[keyTargetURL] = s.TargetURL
	}

	body, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: the GitHub status request does not encode", forge.ErrInvalid)
	}

	return body, nil
}

// statusAnswer is the part of the answer that SetStatus checks.
type statusAnswer struct {
	State   string `json:"state"`
	Context string `json:"context"`
}

// SetStatus sets the status s on the commit sha. GitHub keeps the newest
// status of each context, so a second call with the same context replaces the
// first.
//
// It returns forge.ErrInvalid or forge.ErrTooLarge for a status or a commit ID
// that fails the checks, before any call, forge.ErrNotFound for a missing
// repository or commit, forge.ErrRefused when the credential cannot write
// statuses or GitHub refuses the status, and forge.ErrUnsupported for a
// connection that Capabilities does not list forge.CapStatus for.
func (f *gitHub) SetStatus(ctx context.Context, repo, sha string, s forge.Status) error {
	if err := f.statusAllowed(); err != nil {
		return err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return err
	}

	if idErr := forge.ValidateCommitID(sha); idErr != nil {
		return fmt.Errorf("the commit: %w", idErr)
	}

	if checkErr := checkStatus(s); checkErr != nil {
		return checkErr
	}

	body, err := statusBody(s)
	if err != nil {
		return err
	}

	bearer, err := f.bearer(ctx, r, levelStatus)
	if err != nil {
		return f.forget(r, err)
	}

	resp, err := f.send(ctx, call{
		method: http.MethodPost, path: r.path() + "/statuses/" + sha, bearer: bearer, body: body,
		status: f.statusCallStatus, oversize: nil, target: "",
	})
	if err != nil {
		return f.forget(r, err)
	}

	// The adapter never reports a status that it did not check: the answer
	// must show the state and the context of the request.
	var got statusAnswer
	if decodeErr := decode(resp.body, &got); decodeErr != nil {
		return decodeErr
	}

	if len(resp.body) > maxStatusAnswerBytes || got.State != string(s.State) || got.Context != s.Context {
		return forge.Unavailable(0, "GitHub gave a status that does not match the request")
	}

	return nil
}

// statusCallStatus maps the status of the call that sets a status. GitHub
// answers 422 for a commit that it does not have, and for a commit and a
// context that have the most statuses.
func (f *gitHub) statusCallStatus(r response) error {
	if r.status != http.StatusUnprocessableEntity {
		return f.commitStatus(r)
	}

	var e errorBody
	if decode(r.body, &e) == nil {
		switch {
		case strings.Contains(e.Message, "No commit found"):
			return fmt.Errorf("%w: GitHub has no commit with this ID", forge.ErrNotFound)
		case strings.Contains(e.Message, "maximum number of statuses"):
			return fmt.Errorf("%w: GitHub has the most statuses for this commit and context", forge.ErrRefused)
		}
	}

	return f.commitStatus(r)
}

// checkStatus checks s.
//
// Defense in depth: forge.ValidateStatus checks the status, the caller of the
// port may check it too, and GitHub checks it. This check stays so that a
// status that fails it never goes into a call, also when the check of the port
// changes. It relies on none of them.
func checkStatus(s forge.Status) error {
	switch s.State {
	case forge.StatusPending, forge.StatusSuccess, forge.StatusFailure, forge.StatusError:
	default:
		return fmt.Errorf("%w: the status state is not known", forge.ErrInvalid)
	}

	if s.Context == "" || strings.TrimSpace(s.Context) == "" || !plainLine(s.Context) {
		return fmt.Errorf("%w: the status context is empty or has a character that is not allowed", forge.ErrInvalid)
	}

	if len(s.Context) > forge.MaxStatusContextBytes {
		return fmt.Errorf(
			"%w: the status context has more than %d bytes", forge.ErrTooLarge, forge.MaxStatusContextBytes,
		)
	}

	if !plainLine(s.Description) {
		return fmt.Errorf("%w: the status description has a character that is not allowed", forge.ErrInvalid)
	}

	if len(s.Description) > forge.MaxStatusDescriptionBytes {
		return fmt.Errorf(
			"%w: the status description has more than %d bytes", forge.ErrTooLarge, forge.MaxStatusDescriptionBytes,
		)
	}

	return checkStatusURL(s.TargetURL)
}

// plainLine reports whether text is valid UTF-8 with no control character.
func plainLine(text string) bool {
	return utf8.ValidString(text) && !strings.ContainsFunc(text, func(r rune) bool {
		return unicode.IsControl(r) || r == ' ' || r == ' '
	})
}

// checkStatusURL checks the address of a status: empty, or an https address
// with a host, no user information and no fragment.
func checkStatusURL(raw string) error {
	if raw == "" {
		return nil
	}

	bad := fmt.Errorf("%w: the status address must be an https address with a host", forge.ErrInvalid)

	if len(raw) > forge.MaxStatusURLBytes {
		return fmt.Errorf("%w: the status address has more than %d bytes", forge.ErrTooLarge, forge.MaxStatusURLBytes)
	}

	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return bad
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme != schemeHTTPS || u.Host == "" || u.User != nil || u.Fragment != "" {
		return bad
	}

	return nil
}
