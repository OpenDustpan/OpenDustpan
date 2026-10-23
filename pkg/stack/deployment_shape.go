package stack

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
)

// secretsProvidersMember is the member of a deployment that holds the state of
// its secrets provider.
const secretsProvidersMember = "secrets_providers"

// deploymentFieldKind returns the JSON type that the CLI expects for the
// member name of a deployment, and false for a member that it does not check.
// A member with another type makes the CLI fail to read the whole stack. A null
// value reads as a missing member.
func deploymentFieldKind(name string) (jsontext.Kind, bool) {
	switch name {
	case "manifest", secretsProvidersMember:
		return jsontext.KindBeginObject, true
	case "resources", "pending_operations":
		return jsontext.KindBeginArray, true
	default:
		return jsontext.KindInvalid, false
	}
}

// errDeploymentShape is the cause of a deployment that the CLI cannot read.
var errDeploymentShape = errors.New("the deployment cannot be read")

// checkDeploymentShape returns an error that matches ErrInvalidUpdate when
// deployment is not a JSON object, when a known member of it has the wrong
// JSON type, when an entry of resources or pending_operations is not an
// object, or when other data follows the object. It reads the inner
// deployment of a sequenced save, which a versioned envelope wraps. It streams
// the bytes and builds no tree, and it reads no secret value. It does not
// refuse a repeated member, because the CLI accepts one.
//
// The caller checks the size first, so the scan reads at most the size limit.
func checkDeploymentShape(deployment []byte, seq int64) error {
	inner := innerDeploymentBytes(deployment, seq)

	dec := jsontext.NewDecoder(bytes.NewReader(inner), jsontext.AllowDuplicateNames(true))

	if dec.PeekKind() != jsontext.KindBeginObject {
		return deploymentShapeError("it is not a JSON object")
	}

	if _, err := dec.ReadToken(); err != nil {
		return deploymentShapeError("it is not valid JSON")
	}

	if err := checkDeploymentMembers(dec); err != nil {
		return err
	}

	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return deploymentShapeError("data follows the JSON object")
	}

	return nil
}

// checkDeploymentMembers reads the members of the open object that dec streams
// up to its end and checks the known ones.
func checkDeploymentMembers(dec *jsontext.Decoder) error {
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return deploymentShapeError("it is not valid JSON")
		}

		if tok.Kind() == jsontext.KindEndObject {
			return nil
		}

		if err := checkDeploymentMember(dec, tok.String()); err != nil {
			return err
		}
	}
}

// checkDeploymentMember checks the value of the member name that dec streams,
// when the member is a known one, and reads the value. Each entry of an array
// member (resources and pending_operations) must be an object, as the CLI
// reads them.
func checkDeploymentMember(dec *jsontext.Decoder, name string) error {
	want, known := deploymentFieldKind(name)
	got := dec.PeekKind()

	if known && got != want && got != jsontext.KindNull {
		return deploymentShapeError(fmt.Sprintf("the member %s has the wrong type", name))
	}

	if known && got == jsontext.KindBeginArray {
		return checkDeploymentEntries(dec, name)
	}

	if err := dec.SkipValue(); err != nil {
		return deploymentShapeError("it is not valid JSON")
	}

	return nil
}

// checkDeploymentEntries reads the array of the member name that dec streams
// and checks that each entry is an object. The text names the member and the
// index of the entry, never a value.
func checkDeploymentEntries(dec *jsontext.Decoder, name string) error {
	if _, err := dec.ReadToken(); err != nil {
		return deploymentShapeError("it is not valid JSON")
	}

	for index := 0; dec.PeekKind() != jsontext.KindEndArray; index++ {
		if kind := dec.PeekKind(); kind != jsontext.KindBeginObject {
			if kind == jsontext.KindInvalid {
				return deploymentShapeError("it is not valid JSON")
			}

			return deploymentShapeError(fmt.Sprintf("the entry %d of %s is not an object", index, name))
		}

		if err := dec.SkipValue(); err != nil {
			return deploymentShapeError("it is not valid JSON")
		}
	}

	if _, err := dec.ReadToken(); err != nil {
		return deploymentShapeError("it is not valid JSON")
	}

	return nil
}

// deploymentShapeError returns the refusal of a deployment that the CLI cannot
// read. The text names the rule, never a value of the deployment.
func deploymentShapeError(reason string) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidUpdate, errDeploymentShape, reason)
}
