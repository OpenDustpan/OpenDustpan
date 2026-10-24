package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Journal route path and the journal version the harness offers at the start
// of an update. The Pulumi CLI sends the journal as entries of version 1 and
// offers journal version 2.
const (
	journalPath         = "/journalentries"
	offeredJournalLevel = 2
	// journalUserAgent is the User-Agent of a Pulumi CLI release that the
	// server lets through to journaling.
	journalUserAgent = "pulumi-cli/1 (v3.268.0; linux)"
	// journalGzipFrom is the batch size in bytes from which the harness
	// gzips the body, as the CLI does for a larger batch.
	journalGzipFrom = 1024
	// journalStatePad is the length of the hex padding in the inputs of one
	// resource state: a state of about 1 KiB.
	journalStatePad = 512
	// entriesPerResource is the entry count of one resource step: a begin
	// entry and a success entry.
	entriesPerResource = 2
	// hexPerByte is the hex digit count of one byte.
	hexPerByte = 2
)

// startJournalResponse answers an update start with the lease token and the
// journal version the server chose.
type startJournalResponse struct {
	Token          string `json:"token"`
	JournalVersion int    `json:"journalVersion"`
}

// journaledUpdate is one started update with the journal version of the start.
type journaledUpdate struct {
	id      string
	lease   string
	version int
}

// startJournaledUpdate creates an update on stackName and starts it, offering
// journaling as the Pulumi CLI does.
func (t *target) startJournaledUpdate(ctx context.Context, stackName string) (journaledUpdate, error) {
	base := t.firstURL(stacksPath + t.ns + "/" + loadProject + "/" + stackName)

	createResp, err := t.postUpdate(ctx, base+updatePath, base+updatePath)
	if err != nil {
		return journaledUpdate{}, err
	}

	var created updateIDResponse

	if decodeErr := json.Unmarshal(createResp.body, &created); decodeErr != nil {
		return journaledUpdate{}, fmt.Errorf("decode update ID: %w", decodeErr)
	}

	startURL := base + updatePath + "/" + created.UpdateID

	resp, err := t.send(ctx, request{
		method:    http.MethodPost,
		url:       startURL,
		auth:      pulumiScheme + t.token,
		mediaType: jsonMediaType,
		body:      []byte(`{"journalVersion":` + strconv.Itoa(offeredJournalLevel) + `}`),
	}, sendOptions{userAgent: journalUserAgent, gzipBody: false})
	if err != nil {
		return journaledUpdate{}, err
	}

	if checkErr := check(http.MethodPost, startURL, resp.status, http.StatusOK, resp.body); checkErr != nil {
		return journaledUpdate{}, checkErr
	}

	var started startJournalResponse

	if decodeErr := json.Unmarshal(resp.body, &started); decodeErr != nil {
		return journaledUpdate{}, fmt.Errorf("decode lease token: %w", decodeErr)
	}

	return journaledUpdate{id: created.UpdateID, lease: started.Token, version: started.JournalVersion}, nil
}

// journalResource builds the state of resource n of a stack: about 1 KiB, with
// padding that does not compress to nothing.
func journalResource(stackName string, n int) string {
	label := "res-" + strconv.Itoa(n)
	sum := sha256.Sum256([]byte(label))
	pad := strings.Repeat(hex.EncodeToString(sum[:]), journalStatePad/(hexPerByte*len(sum)))

	return `{"urn":"urn:pulumi:` + stackName + `::` + loadProject + `::aws:s3/bucket:Bucket::` + label +
		`","custom":true,"type":"aws:s3/bucket:Bucket","id":"` + label +
		`","inputs":{"name":"` + label + `","pad":"` + pad + `"},"outputs":{"name":"` + label + `"}}`
}

// journalBatch builds one batch of the entry count entries, starting at the
// resource number first. Each resource is a begin entry and a success entry,
// with sequence IDs that follow without a gap, as the CLI sends them.
func journalBatch(stackName string, first, entries int) []byte {
	var sb strings.Builder

	sb.WriteString(`{"entries":[`)

	for i := range entries / entriesPerResource {
		n := first + i
		op := strconv.Itoa(n + 1)
		seq := n*entriesPerResource + 1

		if i > 0 {
			sb.WriteByte(',')
		}

		sb.WriteString(`{"version":1,"kind":0,"sequenceID":` + strconv.Itoa(seq) + `,"operationID":` + op +
			`,"removeOld":null,"removeNew":null},`)
		sb.WriteString(`{"version":1,"kind":1,"sequenceID":` + strconv.Itoa(seq+1) + `,"operationID":` + op +
			`,"removeOld":null,"removeNew":null,"state":` + journalResource(stackName, n) + `}`)
	}

	sb.WriteString(`]}`)

	return []byte(sb.String())
}

// appendJournal sends one batch and answers its latency.
func (t *target) appendJournal(
	ctx context.Context, stackName string, upd journaledUpdate, body []byte,
) (time.Duration, error) {
	url := t.firstURL(
		stacksPath + t.ns + "/" + loadProject + "/" + stackName + updatePath + "/" + upd.id + journalPath,
	)

	opts := sendOptions{userAgent: journalUserAgent, gzipBody: len(body) >= journalGzipFrom}

	start := time.Now()

	resp, err := t.send(ctx, request{
		method:    http.MethodPatch,
		url:       url,
		auth:      updateScheme + upd.lease,
		mediaType: jsonMediaType,
		body:      body,
	}, opts)
	took := time.Since(start)

	if err != nil {
		return 0, err
	}

	return took, check(http.MethodPatch, url, resp.status, http.StatusOK, resp.body)
}

// measureJournalRun replays one journaled update that sends the batch count
// of batches, each with the entry count of entries, then completes it.
func (t *target) measureJournalRun(
	ctx context.Context, index, entries, batches int,
) (JournalRunResult, bool, error) {
	stackName := "journal-" + strconv.Itoa(index)

	if err := t.createStack(ctx, stackName); err != nil {
		return JournalRunResult{}, false, err
	}

	upd, err := t.startJournaledUpdate(ctx, stackName)
	if err != nil {
		return JournalRunResult{}, false, err
	}

	if upd.version == 0 {
		// The server answers journal version 0: it takes checkpoints only.
		// End the update, so the harness leaves no running update behind.
		return JournalRunResult{}, false, t.completeUpdate(ctx, stackName, upd.id, upd.lease)
	}

	var result JournalRunResult

	result.EntriesPerBatch = entries
	result.Batches = batches

	latencies := make([]float64, 0, batches)
	resourcesPerBatch := entries / entriesPerResource

	peak, runErr := peakAllocDuring(func() error {
		for b := range batches {
			body := journalBatch(stackName, b*resourcesPerBatch, entries)
			result.BatchBytes = len(body)

			took, appendErr := t.appendJournal(ctx, stackName, upd, body)
			if appendErr != nil {
				return appendErr
			}

			latencies = append(latencies, ms(took))
		}

		return nil
	})
	if runErr != nil {
		return JournalRunResult{}, true, runErr
	}

	completeTook, _, completeErr := timed(func() error {
		return t.completeUpdate(ctx, stackName, upd.id, upd.lease)
	})
	if completeErr != nil {
		return JournalRunResult{}, true, completeErr
	}

	result.SentBytes = result.BatchBytes * batches
	result.AppendP50Ms = p50(latencies)
	result.AppendMaxMs = percentile(latencies, 1)
	result.AppendTotalMs = sum(latencies)
	result.CompleteMs = ms(completeTook)
	result.PeakAllocBytes = peak

	return result, true, nil
}

// sum answers the total of samples.
func sum(samples []float64) float64 {
	var total float64

	for _, v := range samples {
		total += v
	}

	return total
}

// measureJournal replays one journaled update for each entry count. When the
// server answers journal version 0, it reports Supported false and sends no
// batch.
func (t *target) measureJournal(ctx context.Context, entries []int, batches int) (JournalResult, error) {
	out := JournalResult{Supported: false, Runs: make([]JournalRunResult, 0, len(entries))}

	for i, count := range entries {
		run, supported, err := t.measureJournalRun(ctx, i, count, batches)
		if err != nil {
			return JournalResult{}, err
		}

		if !supported {
			return JournalResult{Supported: false, Runs: nil}, nil
		}

		out.Supported = true
		out.Runs = append(out.Runs, run)
	}

	return out, nil
}
