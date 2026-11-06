package stack

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SecretsProvidersMember is the member of a deployment that holds the state of
// its secrets provider.
const SecretsProvidersMember = "secrets_providers"

// resourcesMember is the member of a deployment that holds its resources.
const resourcesMember = "resources"

// urnMember is the member of a resource that holds its URN.
const urnMember = "urn"

// deploymentFieldKind returns the name and the JSON type that the CLI expects
// for the member name of a deployment, and false for a member that it does not
// check. The CLI decodes with encoding/json, which matches a member name in
// any case, so a known name matches in any case too, and the returned name is
// the known one. A member with another type makes the CLI fail to read the
// whole stack. A null value reads as a missing member.
func deploymentFieldKind(name string) (string, jsontext.Kind, bool) {
	for _, known := range []string{"manifest", SecretsProvidersMember} {
		if strings.EqualFold(name, known) {
			return known, jsontext.KindBeginObject, true
		}
	}

	for _, known := range []string{resourcesMember, "pending_operations"} {
		if strings.EqualFold(name, known) {
			return known, jsontext.KindBeginArray, true
		}
	}

	return name, jsontext.KindInvalid, false
}

// errDeploymentShape is the cause of a deployment that the CLI cannot read.
var errDeploymentShape = errors.New("the deployment cannot be read")

// envelopeMember is the member of the versioned envelope of a sequenced save
// that holds the deployment.
const envelopeMember = "deployment"

// checkDeploymentShape returns an error that matches ErrInvalidUpdate when
// deployment is not a JSON object, when a known member of it has the wrong
// JSON type, when an entry of resources or pending_operations is not an
// object, when an entry of resources has no urn that is a string, or when
// other data follows the object. It streams the bytes once,
// builds no tree, and reads no secret value. It does not refuse a repeated
// member, because the CLI accepts one.
//
// A sequenced save (seq above 0) is a versioned envelope. The same pass then
// checks each deployment that the envelope holds: the reads serve one of them
// (see innerDeploymentBytes). An envelope with no deployment member is
// refused, because the reads would serve it as an empty deployment and a
// client with a defect could wipe the state of a stack without an error.
//
// The caller checks the size first, so the scan reads at most the size limit.
func checkDeploymentShape(deployment []byte, seq int64) error {
	// The decoder reads a bytes.Buffer in place, so the scan does not copy
	// the deployment.
	//
	// The decoder refuses text that is not valid UTF-8 and a lone surrogate
	// escape such as \ud800. The decoder of the CLI reads both, but the CLI
	// never writes them, because its encoder replaces invalid UTF-8. The
	// check stays strict on purpose: a stored deployment then holds only text
	// that every reader decodes to the same value, and a client that is not
	// the CLI gets a refusal instead of a state that reads back changed.
	dec := jsontext.NewDecoder(bytes.NewBuffer(deployment), jsontext.AllowDuplicateNames(true))

	check := checkDeploymentMember
	hasDeployment := false

	if seq > 0 {
		check = func(dec *jsontext.Decoder, name string) error {
			if strings.EqualFold(name, envelopeMember) {
				hasDeployment = true
			}

			return checkEnvelopeMember(dec, name)
		}
	}

	if err := checkObjectMembers(dec, check); err != nil {
		return err
	}

	if seq > 0 && !hasDeployment {
		return deploymentShapeError("the envelope has no deployment member")
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
// of a deployment.
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
	known, want, isKnown := deploymentFieldKind(name)
	got := dec.PeekKind()

	if isKnown && got != want && got != jsontext.KindNull {
		return deploymentShapeError(fmt.Sprintf("the member %s has the wrong type", known))
	}

	if isKnown && got == jsontext.KindBeginArray {
		return checkDeploymentEntries(dec, known)
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

		if err := checkDeploymentEntry(dec, name, index); err != nil {
			return err
		}
	}

	if _, err := dec.ReadToken(); err != nil {
		return deploymentShapeError("it is not valid JSON")
	}

	return nil
}

// checkDeploymentEntry reads the object at index of the array member name that
// dec streams. An entry of resources must have a urn member that is a string,
// because the CLI reads each resource by its URN. As in the CLI, the name urn
// matches in any case, and each member with that name must be a string. The
// text names the index of the entry, never a value.
func checkDeploymentEntry(dec *jsontext.Decoder, name string, index int) error {
	if name != resourcesMember {
		if err := dec.SkipValue(); err != nil {
			return deploymentShapeError("it is not valid JSON")
		}

		return nil
	}

	hasURN := false

	err := checkObjectMembers(dec, func(dec *jsontext.Decoder, member string) error {
		if strings.EqualFold(member, urnMember) {
			if dec.PeekKind() != jsontext.KindString {
				return deploymentShapeError(fmt.Sprintf("the urn of the entry %d of resources is not a string", index))
			}

			hasURN = true
		}

		if err := dec.SkipValue(); err != nil {
			return deploymentShapeError("it is not valid JSON")
		}

		return nil
	})
	if err != nil {
		return err
	}

	if !hasURN {
		return deploymentShapeError(fmt.Sprintf("the entry %d of resources has no urn", index))
	}

	return nil
}

// deploymentShapeError returns the refusal of a deployment that the CLI cannot
// read. The text names the rule, never a value of the deployment.
func deploymentShapeError(reason string) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidUpdate, errDeploymentShape, reason)
}
