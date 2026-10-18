package gate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

// Result is the result of one evaluation.
type Result string

// The results. ResultError stops the change: it is neither an allow nor a
// denial.
const (
	ResultAllow           Result = "allow"
	ResultDeny            Result = "deny"
	ResultRequireApproval Result = "require-approval"
	ResultError           Result = "error"
)

// Match is a gate or a limit whose condition was satisfied.
type Match struct {
	Gate    string  `json:"gate"`
	Scope   string  `json:"scope"`
	Outcome Outcome `json:"outcome"`
	Mode    Mode    `json:"mode"`
	Message string  `json:"message"`
}

// Approval is one open or closed approval requirement of a record.
type Approval struct {
	Rule string `json:"rule"`
	Need int    `json:"need"`
	Have int    `json:"have"`
	// FreshAuthSeconds is the window of the fresh authentication. Nil asks
	// for none and 0 asks for a new authentication at each approval.
	FreshAuthSeconds *int64   `json:"freshAuthSeconds"`
	From             []string `json:"from"`
	// By holds the people who approved.
	By []string `json:"by,omitempty"`
}

// RecordSubject says what the record decides.
type RecordSubject struct {
	Unit      string `json:"unit"`
	Principal string `json:"principal"`
	Action    string `json:"action,omitempty"`
}

// Record is the decision record of one evaluation. It explains every block.
type Record struct {
	ID             string            `json:"id"`
	Point          DecisionPoint     `json:"point"`
	Schema         int               `json:"schema"`
	Subject        RecordSubject     `json:"subject"`
	InputDigest    string            `json:"inputDigest"`
	PolicyVersions map[string]string `json:"policyVersions"`
	Result         Result            `json:"result"`
	Matched        []Match           `json:"matched"`
	Blockers       []string          `json:"blockers"`
	Approvals      []Approval        `json:"approvals"`
	Errors         []string          `json:"errors"`
	// AutoApply says that the change may apply with no person: the result
	// is allow and no enforced gate warns.
	AutoApply bool `json:"autoApply"`
}

// Cleared reports whether the record lets the change go on: the result is
// allow, or every approval requirement has its count.
func (r Record) Cleared() bool {
	switch r.Result {
	case ResultAllow:
		return true
	case ResultRequireApproval:
		for _, a := range r.Approvals {
			if a.Have < a.Need {
				return false
			}
		}

		return true
	case ResultDeny, ResultError:
		return false
	}

	return false
}

// RecordStore keeps records in memory. It stands in for a table in the
// spike.
type RecordStore struct {
	mu   sync.Mutex
	byID map[string]Record
}

// NewRecordStore returns an empty store.
func NewRecordStore() *RecordStore { return &RecordStore{byID: map[string]Record{}} }

// Put stores r.
func (s *RecordStore) Put(r Record) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID[r.ID] = r
}

// Get returns the record of id, or ErrNotFound.
func (s *RecordStore) Get(_ context.Context, id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.byID[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	return r, nil
}

func newRecordID() string {
	var b [8]byte

	_, err := rand.Read(b[:])
	if err != nil {
		panic("random source failed: " + err.Error())
	}

	return "dec_" + hex.EncodeToString(b[:])
}

func inputDigest(parts ...any) string {
	raw, err := json.Marshal(parts)
	if err != nil {
		return ""
	}

	return digest(string(raw))
}
