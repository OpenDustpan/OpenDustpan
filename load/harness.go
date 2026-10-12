package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Request header and credential scheme names.
const (
	authHeader        = "Authorization"
	contentTypeHeader = "Content-Type"
	pulumiScheme      = "token "
	tfeScheme         = "Bearer "
	updateScheme      = "update-token "
	jsonMediaType     = "application/json"
	streamMediaType   = "application/octet-stream"
)

// Default run dimensions.
const (
	defaultUnits              = 10000
	defaultLockGoroutines     = 16
	defaultLockRounds         = 8
	defaultListReps           = 5
	defaultSeedWorkers        = 32
	defaultContentionAttempts = 64
	defaultHTTPTimeoutSecs    = 120
	defaultTFEPageSize        = 100
)

// Default body sizes in bytes.
const (
	defaultCheckpointSmall = 262144
	defaultCheckpointMid   = 1048576
	defaultCheckpointBig   = 4194304
)

// Load project and stack name details.
const (
	loadProject     = "load"
	stackNameWidth  = 6
	workspacePrefix = "ws-"
	contentionStack = "contend"
	importStackName = "import"
	checkpointStack = "checkpoint"
)

// emptyJSONBody is the create and unlock body the CLIs send when they carry
// no options: the server reads only the fields it names and ignores the rest.
const emptyJSONBody = "{}"

// errDeploymentTooSmall reports a generated deployment too small for a delta edit.
var errDeploymentTooSmall = errors.New("deployment too small for a delta edit")

// errUnexpectedStatus reports a response status the harness did not ask for.
// The wrapped error keeps the method, the URL, both statuses and a body
// prefix for the nightly log.
var errUnexpectedStatus = errors.New("unexpected status")

// Config tunes one harness run.
type Config struct {
	Store              string `json:"store"`
	Namespace          string `json:"namespace"`
	Token              string `json:"-"`
	Units              int    `json:"units"`
	CheckpointSizes    []int  `json:"checkpointSizes"`
	ImportSizes        []int  `json:"importSizes"`
	LockGoroutines     int    `json:"lockGoroutines"`
	LockRounds         int    `json:"lockRounds"`
	ListReps           int    `json:"listReps"`
	SeedWorkers        int    `json:"seedWorkers"`
	ContentionAttempts int    `json:"contentionAttempts"`
}

// DefaultConfig answers the nightly run: 10,000 units and three body sizes.
func DefaultConfig() Config {
	return Config{
		Store:              "sqlite",
		Namespace:          "load",
		Token:              "",
		Units:              defaultUnits,
		CheckpointSizes:    []int{defaultCheckpointSmall, defaultCheckpointMid, defaultCheckpointBig},
		ImportSizes:        []int{defaultCheckpointSmall, defaultCheckpointMid, defaultCheckpointBig},
		LockGoroutines:     defaultLockGoroutines,
		LockRounds:         defaultLockRounds,
		ListReps:           defaultListReps,
		SeedWorkers:        defaultSeedWorkers,
		ContentionAttempts: defaultContentionAttempts,
	}
}

// CheckpointResult holds one checkpoint size: the full save and the delta save.
type CheckpointResult struct {
	SizeBytes           int     `json:"sizeBytes"`
	FullMs              float64 `json:"fullMs"`
	DeltaMs             float64 `json:"deltaMs"`
	FullPeakAllocBytes  uint64  `json:"fullPeakAllocBytes"`
	DeltaPeakAllocBytes uint64  `json:"deltaPeakAllocBytes"`
}

// ImportResult holds one import size.
type ImportResult struct {
	SizeBytes       int     `json:"sizeBytes"`
	ImportMs        float64 `json:"importMs"`
	PeakAllocBytes  uint64  `json:"peakAllocBytes"`
	CheckpointBytes int64   `json:"checkpointBytes"`
}

// LockResult holds the workspace lock contention run.
type LockResult struct {
	Goroutines int     `json:"goroutines"`
	Rounds     int     `json:"rounds"`
	Locks      int     `json:"locks"`
	Conflicts  int     `json:"conflicts"`
	P50Ms      float64 `json:"p50Ms"`
}

// UpdateResult holds the concurrent update run in one namespace.
type UpdateResult struct {
	Attempts  int     `json:"attempts"`
	Started   int     `json:"started"`
	Conflicts int     `json:"conflicts"`
	P50Ms     float64 `json:"p50Ms"`
}

// DatabaseResult holds the update start latency with one and several instances.
type DatabaseResult struct {
	Instances     int     `json:"instances"`
	StoresOpen    int     `json:"storesOpen"`
	FirstP50Ms    float64 `json:"firstP50Ms"`
	SecondP50Ms   float64 `json:"secondP50Ms"`
	SecondPresent bool    `json:"secondPresent"`
}

// ListResult holds the list latency with a full inventory.
type ListResult struct {
	Units               int     `json:"units"`
	UnitsListed         int     `json:"unitsListed"`
	WorkspaceListP50Ms  float64 `json:"workspaceListP50Ms"`
	WorkspaceListFullMs float64 `json:"workspaceListFullMs"`
	InventoryMs         float64 `json:"inventoryMs"`
}

// Report is the machine-readable harness output. Times are milliseconds.
// PeakAllocBytes values are process heap bytes sampled while the server runs
// in-process, so they cover the server and the client together.
type Report struct {
	Time           string             `json:"time"`
	Store          string             `json:"store"`
	Namespace      string             `json:"namespace"`
	Units          int                `json:"units"`
	DurationMs     float64            `json:"durationMs"`
	SeedMs         float64            `json:"seedMs"`
	Checkpoints    []CheckpointResult `json:"checkpoints"`
	Imports        []ImportResult     `json:"imports"`
	WorkspaceLocks LockResult         `json:"workspaceLocks"`
	Updates        UpdateResult       `json:"updates"`
	Database       DatabaseResult     `json:"database"`
	Lists          ListResult         `json:"lists"`
}

// target is one harness run: the server URLs it measures with its credentials.
type target struct {
	urls   []string
	token  string
	ns     string
	client *http.Client
}

// newTarget answers the run target for urls with the harness credentials.
func newTarget(urls []string, token, ns string) target {
	return target{
		urls:  urls,
		token: token,
		ns:    ns,
		client: &http.Client{
			Timeout: defaultHTTPTimeoutSecs * time.Second,
		},
	}
}

// firstURL answers the URL of the first instance with path appended.
func (t *target) firstURL(path string) string {
	return t.urls[0] + path
}

// urlFor answers the URL of instance i with path appended, round-robin.
func (t *target) urlFor(i int, path string) string {
	return t.urls[i%len(t.urls)] + path
}

// request is one harness HTTP call.
type request struct {
	method    string
	url       string
	auth      string
	mediaType string
	body      []byte
}

// response is one harness HTTP answer with its full body.
type response struct {
	status int
	body   []byte
}

// do sends one request and answers its status with the full body. The body
// stays small: the harness reads only its own bounded server answers.
func (t *target) do(ctx context.Context, req request) (response, error) {
	var reader io.Reader

	if len(req.body) > 0 {
		reader = bytes.NewReader(req.body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url, reader)
	if err != nil {
		return response{}, fmt.Errorf("new request: %w", err)
	}

	if req.auth != "" {
		httpReq.Header.Set(authHeader, req.auth)
	}

	if len(req.body) > 0 {
		httpReq.Header.Set(contentTypeHeader, req.mediaType)
	}

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return response{}, fmt.Errorf("do request: %w", err)
	}

	data, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if readErr != nil {
		return response{}, fmt.Errorf("read body: %w", readErr)
	}

	if closeErr != nil {
		return response{}, fmt.Errorf("close body: %w", closeErr)
	}

	return response{status: resp.StatusCode, body: data}, nil
}

// check answers an error when status is not want, with a body prefix.
func check(method, url string, status, want int, body []byte) error {
	if status == want {
		return nil
	}

	const bodyPrefix = 256

	prefix := string(body[:min(len(body), bodyPrefix)])

	return fmt.Errorf(
		"%s %s: status %d, want %d, body %.256s: %w",
		method,
		url,
		status,
		want,
		prefix,
		errUnexpectedStatus,
	)
}

// ms answers d in milliseconds.
func ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// percentile answers the p quantile of samples, or 0 without samples.
func percentile(samples []float64, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}

	ordered := append([]float64(nil), samples...)
	slices.Sort(ordered)

	at := int(math.Round(p * float64(len(ordered)-1)))

	return ordered[at]
}

// p50 answers the median of samples.
func p50(samples []float64) float64 {
	const median = 0.5

	return percentile(samples, median)
}

// peakAllocDuring runs fn and answers the highest heap Alloc sampled while
// it runs. The server runs in-process, so the peak covers both sides.
func peakAllocDuring(fn func() error) (uint64, error) {
	const sampleInterval = time.Millisecond

	stop := make(chan struct{})

	var mu sync.Mutex

	var peak uint64

	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				var mem runtime.MemStats

				runtime.ReadMemStats(&mem)

				mu.Lock()
				peak = max(peak, mem.Alloc)
				mu.Unlock()
			}
		}
	})

	err := fn()

	close(stop)
	wg.Wait()

	return peak, err
}

// timed runs fn once, answers its duration with the peak heap Alloc sampled
// while it runs. The 1 ms sampler also ticks during the run, so every
// latency holds the same small sampling cost.
func timed(fn func() error) (time.Duration, uint64, error) {
	start := time.Now()

	peak, err := peakAllocDuring(fn)

	return time.Since(start), peak, err
}

// buildDeployment answers a deployment of about target bytes: a manifest
// with padded resources shaped like the recorded CLI bodies.
func buildDeployment(targetBytes int) []byte {
	const padSize = 512
	const manifestHead = `{"manifest":{"version":3},"resources":[`
	const manifestTail = `]}`
	const stackResource = `{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",` +
		`"type":"pulumi:pulumi:Stack","outputs":{}}`

	var sb strings.Builder

	sb.WriteString(manifestHead)
	sb.WriteString(stackResource)

	pad := strings.Repeat("x", padSize)

	for n := 0; sb.Len() < targetBytes; n++ {
		sb.WriteByte(',')
		sb.WriteString(`{"urn":"urn:pulumi:dev::web::aws:s3/bucket:Bucket::res-`)
		sb.WriteString(strconv.Itoa(n))
		sb.WriteString(`","type":"aws:s3/bucket:Bucket","custom":true,"id":"res-`)
		sb.WriteString(strconv.Itoa(n))
		sb.WriteString(`","inputs":{"name":"res-`)
		sb.WriteString(strconv.Itoa(n))
		sb.WriteString(`","pad":"`)
		sb.WriteString(pad)
		sb.WriteString(`"},"outputs":{}}`)
	}

	sb.WriteString(manifestTail)

	return []byte(sb.String())
}

// buildState answers a state document of about target bytes with serial and
// lineage set, shaped like the recorded OpenTofu bodies.
func buildState(targetBytes int, serial int64, lineage string) []byte {
	const stateHead = `{"version":4,"terraform_version":"1.9.0","serial":`
	const stateMid = `,"lineage":`
	const stateResources = `,"outputs":{},"resources":[`
	const stateTail = `]}`

	var sb strings.Builder

	sb.WriteString(stateHead)
	sb.WriteString(strconv.FormatInt(serial, 10))
	sb.WriteString(stateMid)

	quoted, err := json.Marshal(lineage)
	if err != nil {
		quoted = []byte(`"load"`)
	}

	sb.Write(quoted)
	sb.WriteString(stateResources)

	const padSize = 256

	pad := strings.Repeat("x", padSize)

	for n := 0; sb.Len() < targetBytes; n++ {
		if n > 0 {
			sb.WriteByte(',')
		}

		sb.WriteString(`{"mode":"managed","type":"null_resource","name":"res-`)
		sb.WriteString(strconv.Itoa(n))
		sb.WriteString(`","provider":"provider[\"registry.terraform.io/hashicorp/null\"]",` +
			`"instances":[{"attributes":{"id":"`)
		sb.WriteString(strconv.Itoa(n))
		sb.WriteString(`","pad":"`)
		sb.WriteString(pad)
		sb.WriteString(`"}}]}`)
	}

	sb.WriteString(stateTail)

	return []byte(sb.String())
}

// deltaPointJSON is one end of a text edit span as the CLI sends it.
type deltaPointJSON struct {
	Offset int `json:"offset"`
}

// deltaSpanJSON is the range of the saved deployment one text edit replaces.
type deltaSpanJSON struct {
	Start deltaPointJSON `json:"start"`
	End   deltaPointJSON `json:"end"`
}

// deltaEditJSON is one text edit of a checkpoint delta request body.
type deltaEditJSON struct {
	Span    deltaSpanJSON `json:"span"`
	NewText string        `json:"newText"`
}

// deltaMade is the result of turning base into a one-edit delta: the delta
// body, the edited text, and the hash of the edited text.
type deltaMade struct {
	delta  []byte
	result []byte
	hash   string
}

// makeDelta answers a one-edit delta turning base into result with the hash
// of the result. The edit replaces 64 bytes at three quarters of the text.
func makeDelta(base []byte) (deltaMade, error) {
	const editLen = 64
	const editAtNumerator = 3
	const editAtDenominator = 4

	if len(base) < editAtDenominator*editLen {
		return deltaMade{}, fmt.Errorf(
			"delta of %d bytes: %w",
			len(base),
			errDeploymentTooSmall,
		)
	}

	at := len(base) * editAtNumerator / editAtDenominator

	filler := bytes.Repeat([]byte("y"), editLen)

	result := make([]byte, 0, len(base))
	result = append(result, base[:at]...)
	result = append(result, filler...)
	result = append(result, base[at+editLen:]...)

	sum := sha256.Sum256(result)

	edit := deltaEditJSON{
		Span: deltaSpanJSON{
			Start: deltaPointJSON{Offset: at},
			End:   deltaPointJSON{Offset: at + editLen},
		},
		NewText: string(filler),
	}

	delta, err := json.Marshal([]deltaEditJSON{edit})
	if err != nil {
		return deltaMade{}, fmt.Errorf("marshal delta: %w", err)
	}

	return deltaMade{delta: delta, result: result, hash: hex.EncodeToString(sum[:])}, nil
}
