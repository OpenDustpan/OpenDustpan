package stack

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strings"
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

// envelopeMember is the member of the versioned envelope of a sequenced save
// that holds the deployment.
const envelopeMember = "deployment"

// checkDeploymentShape returns an error that matches ErrInvalidUpdate when
// deployment is not a JSON object, when a known member of it has the wrong
// JSON type, when an entry of resources or pending_operations is not an
// object, or when other data follows the object. It streams the bytes once,
// builds no tree, and reads no secret value. It does not refuse a repeated
// member, because the CLI accepts one.
//
// A sequenced save can wrap its deployment in a versioned envelope. The same
// pass then checks each deployment that the envelope holds: the reads serve
// one of them (see innerDeploymentBytes), or the envelope itself when it holds
// none.
//
// The caller checks the size first, so the scan reads at most the size limit.
func checkDeploymentShape(deployment []byte, seq int64) error {
	// The decoder reads a bytes.Buffer in place, so the scan does not copy
	// the deployment.
	dec := jsontext.NewDecoder(bytes.NewBuffer(deployment), jsontext.AllowDuplicateNames(true))

	check := checkDeploymentMember
	if seq > 0 {
		check = checkEnvelopeMember
	}

	if err := checkObjectMembers(dec, check); err != nil {
		return err
	}

	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return deploymentShapeError("data follows the JSON object")
	}

	return nil
}

// checkObjectMembers reads the object that dec streams up to its end, and
// calls check for each member with the name of the member, before its value.
// check reads the value.
func checkObjectMembers(dec *jsontext.Decoder, check func(*jsontext.Decoder, string) error) error {
	if dec.PeekKind() != jsontext.KindBeginObject {
		return deploymentShapeError("it is not a JSON object")
	}

	if _, err := dec.ReadToken(); err != nil {
		return deploymentShapeError("it is not valid JSON")
	}

	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return deploymentShapeError("it is not valid JSON")
		}

		if tok.Kind() == jsontext.KindEndObject {
			return nil
		}

		if err := check(dec, tok.String()); err != nil {
			return err
		}
	}
}

// checkEnvelopeMember checks the member name of the object of a sequenced
// save, which can be a versioned envelope, and reads the value. The value of a
// member with the name of envelopeMember, in any case, must be a deployment
// object: encoding/json matches that name in any case, so innerDeploymentBytes
// can serve any of these members. Any other member gets the check of a member
// of a deployment, because the reads serve the envelope itself when it holds
// no deployment.
func checkEnvelopeMember(dec *jsontext.Decoder, name string) error {
	if strings.EqualFold(name, envelopeMember) {
		return checkObjectMembers(dec, checkDeploymentMember)
	}

	return checkDeploymentMember(dec, name)
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
