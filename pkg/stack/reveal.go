package stack

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Reveal of one secret value (ADR 41 decisions 4, 5 and 7): the caller names
// a value path of a checkpoint deployment, the server answers the plain value
// only when the path is in the masked set of the masked state read. The
// masked set here is the union of the masked paths of every resource, built
// with the same maskValue as the read, so no second masking path exists. The
// reveal lookup streams the deployment to the path and decodes only the value
// there, so no tree of the whole deployment is built.

// ErrOtherSecretsProvider reports a stack whose deployment names another
// secrets provider than the service provider. The server cannot decrypt it.
// The error text names the provider, never a value, a key or a scope.
var ErrOtherSecretsProvider = errors.New("stack uses another secrets provider")

// errUnexpectedValue reports streamed JSON of another shape than the lookup
// walks. The state machine of the decoder already rejects malformed JSON, so
// this only guards the Go switch. It carries no value.
var errUnexpectedValue = errors.New("unexpected value")

// serviceSecretsProvider is the only provider the server decrypts: the
// service secrets provider of ADR 23.
const serviceSecretsProvider = "service"

// ciphertextField is the field of a secret object that holds the ciphertext.
const ciphertextField = "ciphertext"

// providersDoc decodes a secrets provider entry.
type providersDoc struct {
	Type string `json:"type"`
}

// SecretsProvider returns the secrets provider type of deployment: the type
// of its secrets_providers entry, or the service provider when the
// deployment names none. Older deployments hold no provider entry and read
// with the service provider. It streams the deployment to the entry and
// skips every other value, so no tree of the deployment is built. A
// deployment that does not decode reads as the service provider.
func SecretsProvider(deployment []byte) string {
	dec := jsontext.NewDecoder(bytes.NewReader(deployment))

	open, err := openStreamedObject(dec)
	if err != nil || !open {
		return serviceSecretsProvider
	}

	for {
		name, end, err := streamedMember(dec)
		if err != nil || end {
			return serviceSecretsProvider
		}

		if name != "secrets_providers" {
			if skipErr := skipStreamed(dec); skipErr != nil {
				return serviceSecretsProvider
			}

			continue
		}

		return streamedProviderType(dec)
	}
}

// streamedProviderType reads the streamed providers object that dec streams
// and returns its type, or the service provider when the object names none.
// Only the providers object is kept, never the deployment.
func streamedProviderType(dec *jsontext.Decoder) string {
	raw, err := dec.ReadValue()
	if err != nil {
		return serviceSecretsProvider
	}

	var providers providersDoc

	if err := json.Unmarshal([]byte(raw), &providers); err != nil || providers.Type == "" {
		return serviceSecretsProvider
	}

	return providers.Type
}

// MaskedPaths returns the masked set of deployment: the value path of each
// secret in the inputs and outputs of every resource. It uses the same
// maskValue as the read, so the reveal check and the read agree.
func MaskedPaths(deployment []byte) ([]string, error) {
	doc, err := decodeDeployment(deployment)
	if err != nil {
		return nil, err
	}

	list := resourceElements(doc)

	var masked []string

	for index, elem := range list {
		obj, ok := elem.(map[string]any)
		if !ok || resourceField(obj, "urn") == "" {
			continue
		}

		inputs := obj["inputs"]
		if inputs == nil {
			inputs = map[string]any{}
		}

		outputs := obj["outputs"]
		if outputs == nil {
			outputs = map[string]any{}
		}

		base := "/resources/" + strconv.Itoa(index)

		maskValue(inputs, base+"/inputs", &masked)
		maskValue(outputs, base+"/outputs", &masked)
	}

	if masked == nil {
		masked = []string{}
	}

	return masked, nil
}

// resourceElements returns the elements of the resources array of doc, or nil
// when doc has no such array. The positions of the elements are the indices
// the reveal path names.
func resourceElements(doc any) []any {
	top, ok := doc.(map[string]any)
	if !ok {
		return nil
	}

	list, ok := top["resources"].([]any)
	if !ok {
		return nil
	}

	return list
}

// splitRevealPath splits an absolute JSON Pointer path into unescaped
// tokens, with the same RFC 6901 escaping as the masked read. Array indices
// stay decimal for the caller to parse.
func splitRevealPath(path string) ([]string, bool) {
	if path == "" || path[0] != '/' {
		return nil, false
	}

	tokens := strings.Split(path[1:], "/")
	for i := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(tokens[i], "~1", "/"), "~0", "~")
	}

	return tokens, true
}

// secretCiphertext returns the ciphertext bytes of the secret object v. A
// value that is not a secret fails with ErrResourceNotFound, so a caller
// cannot probe paths: only a masked path names a secret.
func secretCiphertext(v any) ([]byte, error) {
	if !isSecret(v) {
		return nil, fmt.Errorf("%w: value is not a secret", ErrResourceNotFound)
	}

	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: value is not a secret", ErrResourceNotFound)
	}

	raw, ok := secretCiphertextField(obj)
	if !ok {
		return nil, fmt.Errorf("secret holds no ciphertext: %w", ErrInvalidUpdate)
	}

	cipher, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("secret holds no decodable ciphertext: %w", ErrInvalidUpdate)
	}

	return cipher, nil
}

// secretCiphertextField reads the ciphertext string of a secret object: the
// ciphertext field, or the ciphertext field of the value object of newer
// checkpoints.
func secretCiphertextField(obj map[string]any) (string, bool) {
	if raw, ok := obj[ciphertextField].(string); ok && raw != "" {
		return raw, true
	}

	if inner, ok := obj["value"].(map[string]any); ok {
		if raw, ok := inner[ciphertextField].(string); ok && raw != "" {
			return raw, true
		}
	}

	return "", false
}

// SecretCiphertextOf returns the ciphertext bytes at path of deployment, and
// the masked set holding the path. The server streams the deployment to the
// path and decodes only the value there, so no tree of the whole deployment
// is built. The path must name a secret of the inputs or outputs of one
// resource, else it fails with ErrResourceNotFound, so a caller cannot probe
// paths: the secret check is the masked set check, with the same isSecret as
// the masked read, so no second masking path exists. A secret with no
// decodable ciphertext fails with ErrInvalidUpdate.
func SecretCiphertextOf(deployment []byte, path string) ([]byte, []string, error) {
	tokens, ok := splitRevealPath(path)
	if !ok || len(tokens) < 3 || tokens[0] != "resources" ||
		(tokens[2] != "inputs" && tokens[2] != "outputs") {
		return nil, nil, notFoundCipher(path)
	}

	index, err := strconv.Atoi(tokens[1])
	if err != nil || index < 0 {
		return nil, nil, notFoundCipher(path)
	}

	cipher, err := streamSecretCipher(deployment, index, tokens[2], tokens[3:], path)
	if err != nil {
		return nil, nil, err
	}

	return cipher, []string{path}, nil
}

// notFoundCipher reports a path that names no secret, quoted for the caller
// but with no value. It wraps only the not-found sentinel.
func notFoundCipher(path string) error {
	return fmt.Errorf("%w: %q", ErrResourceNotFound, path)
}

// decodeDeploymentFailed wraps a decoder failure for a deployment that does
// not decode. It carries no value.
func decodeDeploymentFailed(err error) error {
	return fmt.Errorf("decode deployment: %w", err)
}

// openStreamedObject reads the streamed value that dec streams and reports
// whether it opens an object. Any other value is consumed.
func openStreamedObject(dec *jsontext.Decoder) (bool, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return false, decodeDeploymentFailed(err)
	}

	return tok.Kind() == jsontext.KindBeginObject, nil
}

// openStreamedArray reads the streamed value that dec streams and reports
// whether it opens an array. Any other value is consumed.
func openStreamedArray(dec *jsontext.Decoder) (bool, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return false, decodeDeploymentFailed(err)
	}

	return tok.Kind() == jsontext.KindBeginArray, nil
}

// streamedMember reads the next member name of the streamed object that dec
// streams. end reports the closing delimiter.
func streamedMember(dec *jsontext.Decoder) (string, bool, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return "", false, decodeDeploymentFailed(err)
	}

	if tok.Kind() == jsontext.KindEndObject {
		return "", true, nil
	}

	if tok.Kind() != jsontext.KindString {
		return "", false, decodeDeploymentFailed(errUnexpectedValue)
	}

	return tok.String(), false, nil
}

// endOfStreamedArray reports whether the streamed array that dec streams
// closes here, and consumes the delimiter when it does.
func endOfStreamedArray(dec *jsontext.Decoder) (bool, error) {
	if dec.PeekKind() != jsontext.KindEndArray {
		return false, nil
	}

	if _, err := dec.ReadToken(); err != nil {
		return false, decodeDeploymentFailed(err)
	}

	return true, nil
}

// skipStreamed skips the streamed value that dec streams without building
// it. A decoder failure wraps as a decode failure.
func skipStreamed(dec *jsontext.Decoder) error {
	if err := dec.SkipValue(); err != nil {
		return decodeDeploymentFailed(err)
	}

	return nil
}

// streamArrayAt skips the streamed array that dec streams to element index,
// and reports whether the array holds one. The element stays for the caller;
// the rest of the array stays too when the caller returns at once.
func streamArrayAt(dec *jsontext.Decoder, index int) (bool, error) {
	open, err := openStreamedArray(dec)
	if err != nil || !open {
		return false, err
	}

	for i := 0; ; i++ {
		end, err := endOfStreamedArray(dec)
		if err != nil {
			return false, err
		}

		if end {
			return false, nil
		}

		if i == index {
			return true, nil
		}

		if skipErr := skipStreamed(dec); skipErr != nil {
			return false, skipErr
		}
	}
}

// streamSecretCipher walks the streamed deployment to the secret at
// /resources/index/section/rest and returns its ciphertext bytes. Values off
// the path are skipped without building them.
func streamSecretCipher(
	deployment []byte, index int, section string, rest []string, path string,
) ([]byte, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(deployment))

	open, err := openStreamedObject(dec)
	if err != nil {
		return nil, err
	}

	if !open {
		return nil, notFoundCipher(path)
	}

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return nil, err
		}

		if end {
			return nil, notFoundCipher(path)
		}

		if name != "resources" {
			if skipErr := skipStreamed(dec); skipErr != nil {
				return nil, skipErr
			}

			continue
		}

		body, err := resourceBodyAt(dec, deployment, index, path)
		if err != nil {
			return nil, err
		}

		return streamResourceSecret(body, section, rest, path)
	}
}

// resourceBodyAt walks the streamed resources array that dec streams to
// element index and returns the bytes of that resource, from its opening brace
// to the end of deployment. The bytes let the caller read the resource in two
// passes, so nothing of it is kept between the passes. An element that does
// not exist fails with not-found for path.
func resourceBodyAt(
	dec *jsontext.Decoder, deployment []byte, index int, path string,
) ([]byte, error) {
	at, err := streamArrayAt(dec, index)
	if err != nil {
		return nil, err
	}

	if !at {
		return nil, notFoundCipher(path)
	}

	// Only white space and one comma stand between two array elements, so
	// the next brace opens the resource.
	start := int(dec.InputOffset())

	open := bytes.IndexByte(deployment[start:], '{')
	if open < 0 {
		return nil, notFoundCipher(path)
	}

	return deployment[start+open:], nil
}

// streamResourceSecret walks the resource in body to the secret at
// section/rest. The resource answers only with a string URN, as in the masked
// read, so a resource without one has no masked path and answers not-found
// here too. The URN is found in a first pass; the secret is walked in a second.
func streamResourceSecret(
	body []byte, section string, rest []string, path string,
) ([]byte, error) {
	hasURN, err := resourceHasURN(jsontext.NewDecoder(bytes.NewReader(body)))
	if err != nil {
		return nil, err
	}

	if !hasURN {
		return nil, notFoundCipher(path)
	}

	return streamSectionSecret(jsontext.NewDecoder(bytes.NewReader(body)), section, rest, path)
}

// resourceHasURN streams the resource object that dec streams and reports
// whether it holds a non-empty string URN. Every other value is skipped.
func resourceHasURN(dec *jsontext.Decoder) (bool, error) {
	open, err := openStreamedObject(dec)
	if err != nil {
		return false, err
	}

	if !open {
		return false, nil
	}

	for {
		name, end, err := streamedMember(dec)
		if err != nil || end {
			return false, err
		}

		present, err := urnOrSkip(dec, name)
		if err != nil || present {
			return present, err
		}
	}
}

// urnOrSkip reads the streamed member value named name of a resource. A URN
// is checked for a non-empty string; every other value is skipped.
func urnOrSkip(dec *jsontext.Decoder, name string) (bool, error) {
	if name == "urn" {
		return streamedURNPresent(dec)
	}

	return false, skipStreamed(dec)
}

// streamSectionSecret walks the resource object that dec streams to the
// secret at section/rest. The URN is already verified, so the section is
// walked as it streams, and every other member is skipped.
func streamSectionSecret(
	dec *jsontext.Decoder, section string, rest []string, path string,
) ([]byte, error) {
	open, err := openStreamedObject(dec)
	if err != nil {
		return nil, err
	}

	if !open {
		return nil, notFoundCipher(path)
	}

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return nil, err
		}

		if end {
			return nil, notFoundCipher(path)
		}

		if name != section {
			if skipErr := skipStreamed(dec); skipErr != nil {
				return nil, skipErr
			}

			continue
		}

		return streamDescend(dec, rest, path)
	}
}

// streamedURNPresent reads the streamed URN value that dec streams and
// reports whether it is a non-empty string. Any other value is skipped, so
// the resource reads as one without a URN, as in the masked read.
func streamedURNPresent(dec *jsontext.Decoder) (bool, error) {
	if dec.PeekKind() != jsontext.KindString {
		if skipErr := skipStreamed(dec); skipErr != nil {
			return false, skipErr
		}

		return false, nil
	}

	tok, err := dec.ReadToken()
	if err != nil {
		return false, decodeDeploymentFailed(err)
	}

	return tok.String() != "", nil
}

// streamDescend walks the streamed value that dec streams to the value at
// rest and returns the ciphertext bytes when it is a secret. Values off the
// path are skipped without building them.
func streamDescend(dec *jsontext.Decoder, rest []string, path string) ([]byte, error) {
	if len(rest) == 0 {
		return streamTargetSecret(dec, path)
	}

	if dec.PeekKind() == jsontext.KindBeginObject {
		return streamDescendObject(dec, rest, path)
	}

	if dec.PeekKind() == jsontext.KindBeginArray {
		return streamDescendArray(dec, rest, path)
	}

	if skipErr := skipStreamed(dec); skipErr != nil {
		return nil, skipErr
	}

	return nil, notFoundCipher(path)
}

// streamDescendObject walks the streamed object that dec streams to the
// value at rest.
func streamDescendObject(dec *jsontext.Decoder, rest []string, path string) ([]byte, error) {
	if _, err := dec.ReadToken(); err != nil {
		return nil, decodeDeploymentFailed(err)
	}

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return nil, err
		}

		if end {
			return nil, notFoundCipher(path)
		}

		if name != rest[0] {
			if skipErr := skipStreamed(dec); skipErr != nil {
				return nil, skipErr
			}

			continue
		}

		return streamDescend(dec, rest[1:], path)
	}
}

// streamDescendArray walks the streamed array that dec streams to element
// rest[0], then to the value below it.
func streamDescendArray(dec *jsontext.Decoder, rest []string, path string) ([]byte, error) {
	index, err := strconv.Atoi(rest[0])
	if err != nil || index < 0 {
		return nil, notFoundCipher(path)
	}

	at, err := streamArrayAt(dec, index)
	if err != nil {
		return nil, err
	}

	if !at {
		return nil, notFoundCipher(path)
	}

	return streamDescend(dec, rest[1:], path)
}

// streamTargetSecret reads the streamed value that dec streams and returns
// its ciphertext bytes. Only the value at the path is decoded. A value that
// is not a secret fails with ErrResourceNotFound for the path, so a caller
// cannot probe paths.
func streamTargetSecret(dec *jsontext.Decoder, path string) ([]byte, error) {
	raw, err := dec.ReadValue()
	if err != nil {
		return nil, decodeDeploymentFailed(err)
	}

	target := json.NewDecoder(bytes.NewReader(raw))
	target.UseNumber()

	var value any

	if err := target.Decode(&value); err != nil {
		return nil, decodeDeploymentFailed(err)
	}

	cipher, cipherErr := secretCiphertext(value)
	if cipherErr != nil {
		if errors.Is(cipherErr, ErrResourceNotFound) {
			return nil, notFoundCipher(path)
		}

		return nil, cipherErr
	}

	return cipher, nil
}

// PlainValue decodes plaintext bytes of a decrypted secret into a plain JSON
// value: JSON stays JSON, any other plaintext reads as a string.
func PlainValue(plaintext []byte) any {
	dec := json.NewDecoder(bytes.NewReader(plaintext))
	dec.UseNumber()

	var value any

	if err := dec.Decode(&value); err != nil {
		return string(plaintext)
	}

	return value
}
