package textbody

import (
	"bytes"
	"encoding/json"
)

// frame is one open object or array of the body.
type frame struct {
	// keys are the keys that the object named so far. It is nil for an array.
	keys map[string]struct{}
	// wantKey is true when the next token of the object is a key or its end.
	wantKey bool
}

// checkObject returns ErrNotObject when body is not one JSON object, and
// ErrRepeatedKey when an object of body, at any depth, names a key two times.
// It returns ErrJSON for a syntax fault. It reads the first value only: the
// decode of Unmarshal refuses data after it.
//
// The scan holds one frame for each open object or array, on a slice and not
// on the call stack, so a deep body cannot exhaust the stack. The size limit
// of the body limits the number of frames.
func checkObject(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	first, err := dec.Token()
	if err != nil {
		return ErrJSON
	}

	if first != json.Delim('{') {
		return ErrNotObject
	}

	open := []frame{{keys: map[string]struct{}{}, wantKey: true}}

	for len(open) > 0 {
		tok, tokErr := dec.Token()
		if tokErr != nil {
			return ErrJSON
		}

		open, err = step(open, tok)
		if err != nil {
			return err
		}
	}

	return nil
}

// step takes the next token tok of the body and returns the open frames after
// it. It returns ErrRepeatedKey for a key that the open object named before.
func step(open []frame, tok json.Token) ([]frame, error) {
	top := &open[len(open)-1]

	if top.keys != nil && top.wantKey {
		if tok == json.Delim('}') {
			return closeFrame(open), nil
		}

		key, isKey := tok.(string)
		if !isKey {
			return nil, ErrJSON
		}

		if _, seen := top.keys[key]; seen {
			return nil, ErrRepeatedKey
		}

		top.keys[key] = struct{}{}
		top.wantKey = false

		return open, nil
	}

	switch tok {
	case json.Delim('{'):
		return append(open, frame{keys: map[string]struct{}{}, wantKey: true}), nil
	case json.Delim('['):
		return append(open, frame{keys: nil, wantKey: false}), nil
	case json.Delim(']'):
		return closeFrame(open), nil
	default:
		// A scalar value ends the member of an object.
		top.wantKey = top.keys != nil

		return open, nil
	}
}

// closeFrame removes the last frame of open. The closed object or array was
// a value, so an object that holds it then wants its next key.
func closeFrame(open []frame) []frame {
	open = open[:len(open)-1]
	if n := len(open); n > 0 && open[n-1].keys != nil {
		open[n-1].wantKey = true
	}

	return open
}
