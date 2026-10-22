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
// JSON type, or when other data follows the object. It reads the inner
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

		name := tok.String()

		if want, known := deploymentFieldKind(name); known {
			if got := dec.PeekKind(); got != want && got != jsontext.KindNull {
				return deploymentShapeError(fmt.Sprintf("the member %s has the wrong type", name))
			}
		}

		if err := dec.SkipValue(); err != nil {
			return deploymentShapeError("it is not valid JSON")
		}
	}
}

// deploymentShapeError returns the refusal of a deployment that the CLI cannot
// read. The text names the rule, never a value of the deployment.
func deploymentShapeError(reason string) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidUpdate, errDeploymentShape, reason)
}
