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
// most MaxClientBodyBytes. A larger body is an error wrapping
// ErrResponseTooLarge, never a silent truncation.
func decodeCappedJSON(r io.Reader, doc any) error {
	raw, err := io.ReadAll(io.LimitReader(r, MaxClientBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read server answer: %w", err)
	}

	if len(raw) > MaxClientBodyBytes {
		return fmt.Errorf(
			"%w: over %d bytes: check the server log",
			ErrResponseTooLarge,
			MaxClientBodyBytes,
		)
	}

	if err := json.Unmarshal(raw, doc); err != nil {
		return fmt.Errorf("decode server answer: %w", err)
	}

	return nil
}

// decodeErrorAnswer reads an error body of the installation API: the
// message, or the text of the body, or the status alone when the body is
// empty. The body is capped at MaxClientBodyBytes: an oversized body answers
// the status alone.
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
