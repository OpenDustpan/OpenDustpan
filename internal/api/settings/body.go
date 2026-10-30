package settings

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// codeLocationBody is the code location of a unit, in a request and in an
// answer. An empty field is a field that the record does not set.
type codeLocationBody struct {
	Connection string `json:"connection"`
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Directory  string `json:"directory"`
}

// writeBackBody holds the options of the write-back executor.
type writeBackBody struct {
	Delivery string   `json:"delivery"`
	Files    []string `json:"files"`
}

// reviewRequestsBody holds the settings of pull request automation.
type reviewRequestsBody struct {
	Enabled bool `json:"enabled"`
}

// outputBody is a file that a step leaves.
type outputBody struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// stepBody is one custom step. The server sets whether a step changes
// resources, so the body has no field for it.
type stepBody struct {
	Name           string            `json:"name"`
	Role           string            `json:"role"`
	Phase          string            `json:"phase"`
	Args           []string          `json:"args"`
	Env            map[string]string `json:"env"`
	Dir            string            `json:"dir"`
	TimeoutSeconds int64             `json:"timeoutSeconds"`
	AllowFailure   bool              `json:"allowFailure"`
	Outputs        []outputBody      `json:"outputs"`
}

// settingsBody is the settings of a unit, in a request and in an answer. The
// server fixes the unit, from the path. A field that is not in this type fails
// the request.
type settingsBody struct {
	Executor       string             `json:"executor"`
	Pool           string             `json:"pool"`
	CodeLocation   codeLocationBody   `json:"codeLocation"`
	WriteBack      writeBackBody      `json:"writeBack"`
	ReviewRequests reviewRequestsBody `json:"reviewRequests"`
	Steps          []stepBody         `json:"steps"`
}

// stringField is the effective value of a text field and its origin.
type stringField struct {
	Value  string `json:"value"`
	Origin string `json:"origin"`
}

// listField is the effective value of a list of text values and its origin.
type listField struct {
	Value  []string `json:"value"`
	Origin string   `json:"origin"`
}

// boolField is the effective value of a flag and its origin.
type boolField struct {
	Value  bool   `json:"value"`
	Origin string `json:"origin"`
}

// stepsField is the effective custom steps and their origin.
type stepsField struct {
	Value  []stepBody `json:"value"`
	Origin string     `json:"origin"`
}

// effectiveBody is the effective value of each field and where it comes from:
// unit, namespace-default or built-in-default.
type effectiveBody struct {
	Executor       stringField `json:"executor"`
	Pool           stringField `json:"pool"`
	Connection     stringField `json:"connection"`
	Repository     stringField `json:"repository"`
	Ref            stringField `json:"ref"`
	Directory      stringField `json:"directory"`
	Delivery       stringField `json:"delivery"`
	Files          listField   `json:"files"`
	ReviewRequests boolField   `json:"reviewRequests"`
	Steps          stepsField  `json:"steps"`
}

// settingsResponse is the answer to a read and to a write. Version is 0 and
// UpdatedAt is empty for a unit with no record.
type settingsResponse struct {
	UnitID    string        `json:"unitID"`
	Namespace string        `json:"namespace"`
	Version   int64         `json:"version"`
	UpdatedAt string        `json:"updatedAt"`
	Settings  settingsBody  `json:"settings"`
	Effective effectiveBody `json:"effective"`
}

// defaultsBody is the unit defaults of a namespace in a request. Only the pool
// has a default.
type defaultsBody struct {
	Pool string `json:"pool"`
}

// defaultsResponse is the unit defaults of a namespace in an answer.
type defaultsResponse struct {
	Namespace string `json:"namespace"`
	Pool      string `json:"pool"`
	Version   int64  `json:"version"`
	UpdatedAt string `json:"updatedAt"`
}

// suggestionBody is a code location that the git details of an update name.
type suggestionBody struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Source     string `json:"source"`
	UpdateID   string `json:"updateId"`
	ObservedAt string `json:"observedAt"`
}

// suggestionsResponse is the answer of the suggestion route, newest first.
type suggestionsResponse struct {
	Suggestions []suggestionBody `json:"suggestions"`
}

// bulkUnit names a unit of a bulk call and the version of its settings that the
// caller read, 0 for a unit with no record.
type bulkUnit struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

// bulkBody is the request of a bulk call: the units and the one settings body
// that replaces the settings of each.
type bulkBody struct {
	Units    []bulkUnit    `json:"units"`
	Settings *settingsBody `json:"settings"`
}

// bulkError is the error of one item of a bulk call, in the shape of the error
// answer of a call on one unit.
type bulkError struct {
	Code      int            `json:"code"`
	ErrorCode string         `json:"errorCode"`
	ErrorArgs map[string]any `json:"errorArgs,omitempty"`
	Message   string         `json:"message"`
}

// bulkResult is the result of one item. Status is ok or error.
type bulkResult struct {
	ID      string     `json:"id"`
	Status  string     `json:"status"`
	Version int64      `json:"version"`
	Error   *bulkError `json:"error,omitempty"`
}

// bulkResponse holds one result for each unit of the request, in its order.
type bulkResponse struct {
	Results []bulkResult `json:"results"`
}

// The values of bulkResult.Status.
const (
	statusOK    = "ok"
	statusError = "error"
)

// maxTimeoutSeconds is the largest step timeout of a request. The check runs
// before the multiplication into a duration, so a huge number cannot wrap
// into a valid one.
const maxTimeoutSeconds = int64(job.MaxStepTimeout / time.Second)

// toSettings builds the settings of unit from the body. It checks no rule of
// the settings: Validate does. It refuses a timeout that would overflow.
func (b *settingsBody) toSettings(unit identity.Object) (unitsettings.Settings, error) {
	steps, err := stepsOf(b.Steps)
	if err != nil {
		return unitsettings.Settings{}, err
	}

	return unitsettings.Settings{
		Unit:     unit,
		Executor: job.ExecutorKind(b.Executor),
		Pool:     b.Pool,
		CodeLocation: unitsettings.CodeLocation{
			Connection: b.CodeLocation.Connection, Repository: b.CodeLocation.Repository,
			Ref: b.CodeLocation.Ref, Directory: b.CodeLocation.Directory,
		},
		WriteBack:      unitsettings.WriteBack{Delivery: job.Delivery(b.WriteBack.Delivery), Files: b.WriteBack.Files},
		ReviewRequests: unitsettings.ReviewRequests{Enabled: b.ReviewRequests.Enabled},
		Steps:          steps,
		Version:        0,
		UpdatedAt:      time.Time{},
	}, nil
}

func stepsOf(in []stepBody) ([]job.Step, error) {
	if len(in) == 0 {
		return nil, nil
	}

	out := make([]job.Step, 0, len(in))

	for i := range in {
		s := &in[i]
		if s.TimeoutSeconds < 0 || s.TimeoutSeconds > maxTimeoutSeconds {
			return nil, fmt.Errorf("%w: step %d: the timeout is 0 to %d seconds", unitsettings.ErrInvalid, i,
				maxTimeoutSeconds)
		}

		step := job.Step{
			Name: s.Name, Role: job.StepRole(s.Role), Phase: job.PhaseKind(s.Phase), Args: s.Args, Env: s.Env,
			Dir: s.Dir, Timeout: time.Duration(s.TimeoutSeconds) * time.Second, AllowFailure: s.AllowFailure,
			ChangesResources: false, Outputs: nil,
		}

		for _, o := range s.Outputs {
			step.Outputs = append(step.Outputs, job.Output{Kind: job.OutputKind(o.Kind), Path: o.Path})
		}

		out = append(out, step)
	}

	return out, nil
}

func stepBodies(in []job.Step) []stepBody {
	out := make([]stepBody, 0, len(in))

	for i := range in {
		s := &in[i]
		b := stepBody{
			Name: s.Name, Role: string(s.Role), Phase: string(s.Phase), Args: nonNil(s.Args), Env: s.Env, Dir: s.Dir,
			TimeoutSeconds: int64(s.Timeout / time.Second), AllowFailure: s.AllowFailure,
			Outputs: make([]outputBody, 0, len(s.Outputs)),
		}

		if b.Env == nil {
			b.Env = map[string]string{}
		}

		for _, o := range s.Outputs {
			b.Outputs = append(b.Outputs, outputBody{Kind: string(o.Kind), Path: o.Path})
		}

		out = append(out, b)
	}

	return out
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

// wireTime writes a time as the answers of the API do, or "" for the zero time.
func wireTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}

// settingsOf makes the answer from a view.
func settingsOf(v *unitsettingssvc.View) settingsResponse {
	s := &v.Settings
	e := &v.Effective

	str := func(f unitsettings.Field[string]) stringField {
		return stringField{Value: f.Value, Origin: string(f.Origin)}
	}

	return settingsResponse{
		UnitID: s.Unit.UnitID(), Namespace: s.Unit.Namespace().String(), Version: s.Version,
		UpdatedAt: wireTime(s.UpdatedAt),
		Settings: settingsBody{
			Executor: string(s.Executor), Pool: s.Pool,
			CodeLocation: codeLocationBody{
				Connection: s.CodeLocation.Connection, Repository: s.CodeLocation.Repository,
				Ref: s.CodeLocation.Ref, Directory: s.CodeLocation.Directory,
			},
			WriteBack:      writeBackBody{Delivery: string(s.WriteBack.Delivery), Files: nonNil(s.WriteBack.Files)},
			ReviewRequests: reviewRequestsBody{Enabled: s.ReviewRequests.Enabled},
			Steps:          stepBodies(s.Steps),
		},
		Effective: effectiveBody{
			Executor:       stringField{Value: string(e.Executor.Value), Origin: string(e.Executor.Origin)},
			Pool:           str(e.Pool),
			Connection:     str(e.Connection),
			Repository:     str(e.Repository),
			Ref:            str(e.Ref),
			Directory:      str(e.Directory),
			Delivery:       stringField{Value: string(e.Delivery.Value), Origin: string(e.Delivery.Origin)},
			Files:          listField{Value: nonNil(e.Files.Value), Origin: string(e.Files.Origin)},
			ReviewRequests: boolField{Value: e.ReviewRequests.Value, Origin: string(e.ReviewRequests.Origin)},
			Steps:          stepsField{Value: stepBodies(e.Steps.Value), Origin: string(e.Steps.Origin)},
		},
	}
}
