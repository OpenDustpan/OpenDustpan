package dustpan

import (
	"encoding/json"
	"net/http"

	"opendustpan.org/opendustpan/internal/errcode"
)

// routeError is the error body of an answer that the SDK gives by itself, in
// the shape of the tool-neutral API: the status in code and the machine code
// in errorCode. None of these answers has arguments.
type routeError struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
}

// routeAnswer returns the answer of code, a catalog code without arguments.
// A code that the catalog refuses is a bug here, and the answer is then the
// internal failure.
func routeAnswer(code string) errcode.Answer {
	answer, err := errcode.New(code)
	if err != nil {
		return errcode.Internal()
	}

	return answer
}

// writeRouteError answers status with message and the code of answer.
func writeRouteError(w http.ResponseWriter, status int, message string, answer errcode.Answer) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	doc := routeError{Message: message, Code: status, ErrorCode: answer.Code()}
	if err := json.NewEncoder(w).Encode(doc); err != nil {
		// The client has gone; there is nothing to report.
		return
	}
}
