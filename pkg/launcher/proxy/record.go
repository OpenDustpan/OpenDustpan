package proxy

import (
	"errors"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// DefaultRecordBuffer is the number of records that the job proxy keeps when
// the sink is slow or fails. When the buffer is full, the job proxy refuses
// new requests.
const DefaultRecordBuffer = 10000

// ErrRecordsLost is the error of Close when records were in the buffer that
// the sink did not take.
var ErrRecordsLost = errors.New("the job proxy closed with records that the sink did not take")

// Reason is the fixed reason of a record. A record holds a Reason, never the
// text of a library error.
type Reason string

// The reasons of a record. The reasons of jobproxy.Match keep their own
// spelling.
const (
	// ReasonNone is the reason of a request that passed.
	ReasonNone Reason = ""
	// ReasonBadTarget: the CONNECT target is not a valid host and port.
	ReasonBadTarget Reason = "bad-target"
	// ReasonBadRequest: the request is not a request that the job proxy
	// serves.
	ReasonBadRequest Reason = "bad-request"
	// ReasonNoRuleHost: no proxy rule names the host and the port.
	ReasonNoRuleHost Reason = Reason(jobproxy.ReasonNoRuleHost)
	// ReasonNoRuleMatched: no proxy rule allows the method and the path.
	ReasonNoRuleMatched Reason = Reason(jobproxy.ReasonNoRuleMatched)
	// ReasonPathNotCanonical: the path is not canonical.
	ReasonPathNotCanonical Reason = Reason(jobproxy.ReasonPathNotCanonical)
	// ReasonAuthOverHTTP: the request would send a credential in plain HTTP.
	ReasonAuthOverHTTP Reason = Reason(jobproxy.ReasonPlainHTTPInjection)
	// ReasonHostMismatch: the SNI, the Host header or :authority is not the
	// host of the CONNECT.
	ReasonHostMismatch Reason = "host-mismatch"
	// ReasonModeNotAllowed: the mode of the proxy rule does not serve this
	// kind of request.
	ReasonModeNotAllowed Reason = "mode-not-allowed"
	// ReasonGuardRefused: the outbound guard refused an address of the host.
	ReasonGuardRefused Reason = "guard-refused"
	// ReasonResolveFailed: the host did not resolve.
	ReasonResolveFailed Reason = "resolve-failed"
	// ReasonDialFailed: the connection to the host failed.
	ReasonDialFailed Reason = "dial-failed"
	// ReasonUpstreamFailed: the host did not give a valid response.
	ReasonUpstreamFailed Reason = "upstream-failed"
	// ReasonAuthUnavailable: the credential of the proxy rule is not
	// available or is not valid.
	ReasonAuthUnavailable Reason = "credential-unavailable"
	// ReasonAuthInResponse: the response holds a form of the credential.
	ReasonAuthInResponse Reason = "credential-in-response"
	// ReasonEncodedResponse: the response of a proxy rule with a credential
	// has a content encoding, which the scan cannot read.
	ReasonEncodedResponse Reason = "encoded-response"
	// ReasonBodyTooLarge: the request body is over the limit.
	ReasonBodyTooLarge Reason = "body-too-large"
	// ReasonTooBusy: the job proxy has too many connections.
	ReasonTooBusy Reason = "too-busy"
	// ReasonTimeout: a time limit ended the request.
	ReasonTimeout Reason = "timeout"
)

// Record is the record of one request. It holds no header, no body, no query
// value, no credential and no text of a library error.
type Record struct {
	Job       string
	Phase     string
	Unit      string
	Operation jobproxy.Operation
	Mode      jobproxy.Mode
	Method    string
	Host      string
	// Path is the path of the request when it is canonical, and empty
	// otherwise.
	Path    string
	RuleID  string
	Allowed bool
	Reason  Reason
	Status  int
	Bytes   int64
	Time    time.Time
}

// Records is the sink of the records. Write must not keep the pointer to
// anything in the Record: it holds values only.
type Records interface {
	Write(rec Record) error
}

// recordQueue is the bounded buffer between the requests and the sink. A
// request reserves a slot before it does any work, so no request passes
// without room for its record.
type recordQueue struct {
	slots chan struct{}
	queue chan Record
	sink  Records
	retry time.Duration
	done  chan struct{}
	wg    sync.WaitGroup
}

// newRecordQueue starts the goroutine that writes the records to sink.
func newRecordQueue(sink Records, size int, retry time.Duration) *recordQueue {
	q := &recordQueue{
		slots: make(chan struct{}, size),
		queue: make(chan Record, size),
		sink:  sink,
		retry: retry,
		done:  make(chan struct{}),
		wg:    sync.WaitGroup{},
	}

	q.wg.Add(1)

	go q.drain()

	return q
}

// reserve takes one slot. It returns false when the buffer is full.
func (q *recordQueue) reserve() bool {
	select {
	case q.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// put queues a record for a slot that reserve gave. It never blocks, because
// the queue is as large as the slots.
func (q *recordQueue) put(r Record) {
	q.queue <- r
}

// drain writes the records in order. A record that the sink refuses stays
// first, and the next try comes after the retry time.
func (q *recordQueue) drain() {
	defer q.wg.Done()

	for {
		select {
		case <-q.done:
			return
		case r := <-q.queue:
			q.writeUntilTaken(r)
		}
	}
}

// writeUntilTaken retries one record until the sink takes it or the queue
// closes. It returns without a write when the queue closes.
func (q *recordQueue) writeUntilTaken(r Record) {
	for {
		if q.sink.Write(r) == nil {
			<-q.slots

			return
		}

		select {
		case <-q.done:
			// The record is lost. close counts it.
			q.requeue(r)

			return
		case <-time.After(q.retry):
		}
	}
}

// requeue puts a record back so that close counts it.
func (q *recordQueue) requeue(r Record) {
	q.queue <- r
}

// close stops the goroutine, tries one write for each record that is left and
// returns ErrRecordsLost if the sink refuses any.
func (q *recordQueue) close() error {
	close(q.done)
	q.wg.Wait()

	lost := false

	for {
		select {
		case r := <-q.queue:
			if q.sink.Write(r) != nil {
				lost = true
			}
		default:
			if lost {
				return ErrRecordsLost
			}

			return nil
		}
	}
}
