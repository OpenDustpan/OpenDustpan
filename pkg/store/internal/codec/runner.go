package codec

import (
	"encoding/json"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// RunnerPoolRow is the column representation of a runner pool. NamespaceID is
// the stable ID of the namespace, and NamespaceName its name now. LeaseMs and
// Concurrency are 0 when the pool takes the default.
type RunnerPoolRow struct {
	ID            string
	NamespaceID   string
	NamespaceName string
	Name          string
	Purpose       string
	LeaseMs       int64
	Concurrency   int64
	Isolation     string
	GitMode       string
	GitHelper     string
	CreatedAtMs   int64
	UpdatedAtMs   int64
}

// RunnerRow is the column representation of a runner. A time is 0 when it is
// not set.
type RunnerRow struct {
	ID                     string
	NamespaceID            string
	NamespaceName          string
	PoolID                 string
	Name                   string
	Kind                   string
	Isolation              string
	Labels                 string
	Release                string
	KeyPublic              []byte
	KeyCreatedAtMs         int64
	PreviousKeyPublic      []byte
	PreviousKeyCreatedAtMs int64
	RotatedAtMs            int64
	JoinedWith             string
	Draining               bool
	RetiredAtMs            int64
	LastSeenAtMs           int64
	JoinedAtMs             int64
}

// RunnerJoinTokenRow is the column representation of a join token.
type RunnerJoinTokenRow struct {
	ID          string
	NamespaceID string
	PoolID      string
	Sha256      string
	CreatedBy   string
	CreatedAtMs int64
	ExpiresAtMs int64
	UsedAtMs    int64
	UsedBy      string
	RevokedAtMs int64
}

// RunnerHandoffTokenRow is the column representation of a handoff token.
type RunnerHandoffTokenRow struct {
	Sha256      string
	OperationID string
	Token       int64
	RunnerID    string
	IssuedAtMs  int64
	ExpiresAtMs int64
	UsedAtMs    int64
}

// RunnerMillis encodes a time, with 0 for the zero time.
func RunnerMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// RunnerTime decodes a time that RunnerMillis encoded.
func RunnerTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}

// PoolToRow encodes the settings of p. The caller sets the namespace.
func PoolToRow(p *runner.Pool) (RunnerPoolRow, error) {
	isolation, err := json.Marshal(append([]runner.Isolation{}, p.Isolation...))
	if err != nil {
		return RunnerPoolRow{}, fmt.Errorf("encode isolation: %w", err)
	}

	mode := p.GitCredentials.Mode
	if mode == "" {
		mode = runner.GitCredentialsNone
	}

	return RunnerPoolRow{
		ID: p.ID, NamespaceID: "", NamespaceName: p.Namespace.String(), Name: p.Name,
		Purpose: string(p.Purpose), LeaseMs: p.Lease.Milliseconds(), Concurrency: int64(p.Concurrency),
		Isolation: string(isolation), GitMode: string(mode), GitHelper: p.GitCredentials.Helper,
		CreatedAtMs: RunnerMillis(p.CreatedAt), UpdatedAtMs: RunnerMillis(p.UpdatedAt),
	}, nil
}

// PoolFromRow decodes a pool.
func PoolFromRow(r RunnerPoolRow) (runner.Pool, error) {
	var isolation []runner.Isolation
	if err := json.Unmarshal([]byte(r.Isolation), &isolation); err != nil {
		return runner.Pool{}, fmt.Errorf("decode isolation of pool %s: %w", r.ID, err)
	}

	if len(isolation) == 0 {
		isolation = nil
	}

	return runner.Pool{
		ID: r.ID, Namespace: namespace.Name(r.NamespaceName), Name: r.Name, Purpose: runner.Purpose(r.Purpose),
		Lease: time.Duration(r.LeaseMs) * time.Millisecond, Concurrency: int(r.Concurrency),
		Isolation:      isolation,
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialMode(r.GitMode), Helper: r.GitHelper},
		CreatedAt:      RunnerTime(r.CreatedAtMs), UpdatedAt: RunnerTime(r.UpdatedAtMs),
	}, nil
}

// JoinTokenFromRow decodes a join token.
func JoinTokenFromRow(r RunnerJoinTokenRow) runner.JoinToken {
	return runner.JoinToken{
		ID: r.ID, Pool: r.PoolID, SHA256: r.Sha256, CreatedBy: r.CreatedBy, CreatedAt: RunnerTime(r.CreatedAtMs),
		ExpiresAt: RunnerTime(r.ExpiresAtMs), UsedAt: RunnerTime(r.UsedAtMs), UsedBy: r.UsedBy,
		RevokedAt: RunnerTime(r.RevokedAtMs),
	}
}

// ReleaseFromString decodes the release of a runner, with the zero release
// for the empty string.
func ReleaseFromString(s string) (runner.Release, error) {
	if s == "" {
		return runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, nil
	}

	rel, err := runner.ParseRelease(s)
	if err != nil {
		return runner.Release{}, fmt.Errorf("decode release: %w", err)
	}

	return rel, nil
}

// RunnerFromRow decodes a runner.
func RunnerFromRow(r RunnerRow) (runner.Runner, error) {
	var labels map[string]string
	if err := json.Unmarshal([]byte(r.Labels), &labels); err != nil {
		return runner.Runner{}, fmt.Errorf("decode labels of runner %s: %w", r.ID, err)
	}

	if len(labels) == 0 {
		labels = nil
	}

	rel, err := ReleaseFromString(r.Release)
	if err != nil {
		return runner.Runner{}, err
	}

	prev := runner.Key{Public: nil, CreatedAt: time.Time{}}
	if len(r.PreviousKeyPublic) != 0 {
		prev = runner.Key{Public: r.PreviousKeyPublic, CreatedAt: RunnerTime(r.PreviousKeyCreatedAtMs)}
	}

	return runner.Runner{
		ID:          r.ID,
		Pool:        r.PoolID,
		Namespace:   namespace.Name(r.NamespaceName),
		Name:        r.Name,
		Kind:        runner.Kind(r.Kind),
		Isolation:   runner.Isolation(r.Isolation),
		Labels:      labels,
		Release:     rel,
		Key:         runner.Key{Public: r.KeyPublic, CreatedAt: RunnerTime(r.KeyCreatedAtMs)},
		PreviousKey: prev,
		RotatedAt:   RunnerTime(r.RotatedAtMs),
		JoinedWith:  r.JoinedWith,
		Draining:    r.Draining,
		RetiredAt: RunnerTime(
			r.RetiredAtMs,
		),
		LastSeenAt: RunnerTime(r.LastSeenAtMs),
		JoinedAt:   RunnerTime(r.JoinedAtMs),
	}, nil
}

// LabelsToJSON encodes the labels of a runner.
func LabelsToJSON(labels map[string]string) (string, error) {
	if labels == nil {
		labels = map[string]string{}
	}

	b, err := json.Marshal(labels)
	if err != nil {
		return "", fmt.Errorf("encode labels: %w", err)
	}

	return string(b), nil
}

// HandoffTokenFromRow decodes a handoff token.
func HandoffTokenFromRow(r RunnerHandoffTokenRow) runner.HandoffToken {
	return runner.HandoffToken{
		SHA256: r.Sha256, Operation: r.OperationID, Token: r.Token, Runner: r.RunnerID,
		IssuedAt: RunnerTime(r.IssuedAtMs), ExpiresAt: RunnerTime(r.ExpiresAtMs), UsedAt: RunnerTime(r.UsedAtMs),
	}
}

// PoolOperationKinds returns the two durable kinds that name the pool id: the
// kind of its job phases, then the kind of its sweeps.
func PoolOperationKinds(id string) [2]durable.Kind {
	return [2]durable.Kind{job.RunnerKind(id), runner.SweepKind(id)}
}
