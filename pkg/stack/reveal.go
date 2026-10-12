package stack

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Reveal of one secret value (ADR 41 decisions 4, 5 and 7): the caller names
// a value path of a checkpoint deployment, the server answers the plain value
// only when the path is in the masked set of the masked state read. The
// masked set here is the union of the masked paths of every resource, built
// with the same maskValue as the read, so no second masking path exists.

// ErrOtherSecretsProvider reports a stack whose deployment names another
// secrets provider than the service provider. The server cannot decrypt it.
// The error text names the provider, never a value, a key or a scope.
var ErrOtherSecretsProvider = errors.New("stack uses another secrets provider")

// serviceSecretsProvider is the only provider the server decrypts: the
// service secrets provider of ADR 23.
const serviceSecretsProvider = "service"

// ciphertextField is the field of a secret object that holds the ciphertext.
const ciphertextField = "ciphertext"

// SecretsProvider returns the secrets provider type of deployment: the type
// of its secrets_providers entry, or the service provider when the
// deployment names none. Older deployments hold no provider entry and read
// with the service provider.
func SecretsProvider(deployment []byte) string {
	var doc map[string]any
	if err := json.Unmarshal(deployment, &doc); err != nil {
		return serviceSecretsProvider
	}

	providers, ok := doc["secrets_providers"].(map[string]any)
	if !ok {
		return serviceSecretsProvider
	}

	provider, ok := providers["type"].(string)
	if !ok || provider == "" {
		return serviceSecretsProvider
	}

	return provider
}

// MaskedPaths returns the masked set of deployment: the value path of each
// secret in the inputs and outputs of every resource. It uses the same
// maskValue as the read, so the reveal check and the read agree.
func MaskedPaths(deployment []byte) ([]string, error) {
	doc, err := decodeDeployment(deployment)
	if err != nil {
		return nil, err
	}

	entries, _ := resourceList(doc)

	var masked []string

	for _, obj := range entries {
		index := resourceIndex(doc, obj)

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

// resourceIndex returns the position of obj in the resources array of doc,
// or zero when the document has no such array.
func resourceIndex(doc any, obj map[string]any) int {
	top, ok := doc.(map[string]any)
	if !ok {
		return 0
	}

	list, ok := top["resources"].([]any)
	if !ok {
		return 0
	}

	for i, elem := range list {
		if other, ok := elem.(map[string]any); ok && other["urn"] == obj["urn"] {
			return i
		}
	}

	return 0
}

// lookupPointer returns the value at the JSON Pointer path in doc. The path
// is absolute, with RFC 6901 escaping. Array indices are decimal.
func lookupPointer(doc any, path string) (any, bool) {
	if path == "" || path[0] != '/' {
		return nil, false
	}

	tokens := strings.Split(path[1:], "/")
	for i := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(tokens[i], "~1", "/"), "~0", "~")
	}

	node := doc

	for _, token := range tokens {
		switch typed := node.(type) {
		case map[string]any:
			next, ok := typed[token]
			if !ok {
				return nil, false
			}

			node = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}

			node = typed[index]
		default:
			return nil, false
		}
	}

	return node, true
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
// the masked set of the deployment. The path must be in the masked set, else
// it fails with ErrResourceNotFound, so a caller cannot probe paths. A path
// that names no secret fails the same way. A secret with no decodable
// ciphertext fails with ErrInvalidUpdate.
func SecretCiphertextOf(deployment []byte, path string) ([]byte, []string, error) {
	masked, err := MaskedPaths(deployment)
	if err != nil {
		return nil, nil, err
	}

	if !slices.Contains(masked, path) {
		return nil, masked, fmt.Errorf("%w: %q", ErrResourceNotFound, path)
	}

	doc, err := decodeDeployment(deployment)
	if err != nil {
		return nil, masked, err
	}

	node, ok := lookupPointer(doc, path)
	if !ok {
		return nil, masked, fmt.Errorf("%w: %q", ErrResourceNotFound, path)
	}

	cipher, err := secretCiphertext(node)
	if err != nil {
		if errors.Is(err, ErrResourceNotFound) {
			return nil, masked, fmt.Errorf("%w: %q", ErrResourceNotFound, path)
		}

		return nil, masked, err
	}

	return cipher, masked, nil
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
