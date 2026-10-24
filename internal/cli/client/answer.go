package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// errorAnswer is the error body of the installation API. Only the message is
// read; the code repeats the status.
type errorAnswer struct {
	Message string `json:"message"`
}

// decodeCappedJSON decodes one installation API answer from r, reading at
// most limit bytes. A larger body is an error wrapping ErrResponseTooLarge,
// never a silent truncation. The error holds no advice: each caller adds the
// one command that helps.
func decodeCappedJSON(r io.Reader, limit int64, doc any) error {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return fmt.Errorf("read server answer: %w", err)
	}

	if int64(len(raw)) > limit {
		return fmt.Errorf("%w: over %d bytes", ErrResponseTooLarge, limit)
	}

	if err := json.Unmarshal(raw, doc); err != nil {
		return fmt.Errorf("decode server answer: %w", err)
	}

	return nil
}

// answerText returns the text of a server answer with an unexpected status:
// message, which holds the status and the message of the server once, and
// then hint, the step that can help. A 4xx answer has its cause in the
// request: the same command fails again and the server log adds nothing, so
// the text of a 4xx answer has no hint.
func answerText(status int, message, hint string) string {
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		return message
	}

	return message + ": " + hint
}

// decodeErrorAnswer reads an error body of the installation API: the
// message, or the text of the body when it is not JSON. It answers the status
// alone when the body is empty, when the body is larger than
// MaxClientBodyBytes, or when the read fails.
func decodeErrorAnswer(resp *http.Response) string {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxClientBodyBytes+1))
	if err != nil {
		return resp.Status
	}

	if len(raw) > MaxClientBodyBytes {
		return resp.Status
	}

	var doc errorAnswer

	if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil || doc.Message == "" {
		if text := strings.TrimSpace(string(raw)); text != "" {
			return fmt.Sprintf("%s: %s", resp.Status, text)
		}

		return resp.Status
	}

	return fmt.Sprintf("%s: %s", resp.Status, doc.Message)
}
