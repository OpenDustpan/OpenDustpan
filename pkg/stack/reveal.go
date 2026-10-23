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

// Memory bound: heap growth of the lookup does not depend on the size of the
// deployment. It grows with the largest single string token of the deployment,
// about four times that string, because jsontext buffers a string token whole
// while it skips it. A deployment with a 1 MiB string may cost about 4 MiB
// more per call.
//
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

		if name != secretsProvidersMember {
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
	for i, token := range tokens {
		if !validEscapes(token) {
			return nil, false
		}

		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}

	return tokens, true
}

// validEscapes reports whether every "~" in token starts an RFC 6901 escape,
// "~0" or "~1". Any other "~" is not a canonical path.
func validEscapes(token string) bool {
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			continue
		}

		if i+1 >= len(token) || (token[i+1] != '0' && token[i+1] != '1') {
			return false
		}

		i++
	}

	return true
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

	index, ok := parseIndex(tokens[1])
	if !ok {
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
// the path are skipped without building them. An answer is given only after
// every member of the deployment has streamed, so a duplicate key anywhere
// fails the lookup, as it fails the masked read's decoder in pkg/workspace.
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

	cipher, found, err := seekMember(dec, "resources", func() ([]byte, error) {
		body, bodyErr := resourceBodyAt(dec, deployment, index, path)
		if bodyErr != nil {
			return nil, bodyErr
		}

		return streamResourceSecret(body, section, rest, path)
	})
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, notFoundCipher(path)
	}

	// The resource element and the rest of the document stream after the
	// answer, so the decoder checks every member.
	if err := drainAfterElement(dec); err != nil {
		return nil, err
	}

	return cipher, nil
}

// seekMember streams the members of the object that dec streams until the
// member named name. Every other member is skipped. It calls visit at the
// value of that member and returns its result. found is false when the object
// has no such member. The caller drains the rest of the object after an
// answer.
func seekMember(
	dec *jsontext.Decoder, name string, visit func() ([]byte, error),
) ([]byte, bool, error) {
	for {
		member, end, err := streamedMember(dec)
		if err != nil || end {
			return nil, false, err
		}

		if member != name {
			if skipErr := skipStreamed(dec); skipErr != nil {
				return nil, false, skipErr
			}

			continue
		}

		cipher, err := visit()

		return cipher, true, err
	}
}

// drainAfterElement skips the resource element at the decoder position, the
// rest of the resources array and the rest of the top-level object. It keeps
// nothing.
func drainAfterElement(dec *jsontext.Decoder) error {
	if err := skipStreamed(dec); err != nil {
		return err
	}

	if err := drainStreamedArray(dec); err != nil {
		return err
	}

	return drainStreamedObject(dec)
}

// drainStreamedArray skips the rest of the streamed array that dec streams.
func drainStreamedArray(dec *jsontext.Decoder) error {
	for {
		end, err := endOfStreamedArray(dec)
		if err != nil || end {
			return err
		}

		if err := skipStreamed(dec); err != nil {
			return err
		}
	}
}

// drainStreamedObject skips the rest of the streamed object that dec streams.
func drainStreamedObject(dec *jsontext.Decoder) error {
	for {
		_, end, err := streamedMember(dec)
		if err != nil || end {
			return err
		}

		if err := skipStreamed(dec); err != nil {
			return err
		}
	}
}

// resourceBodyAt walks the streamed resources array that dec streams to
// element index and returns the bytes of that resource, from its opening brace
// to the end of deployment. The bytes let the caller read the resource in two
// passes, so nothing of it is kept between the passes. An element that is not
// an object has no masked path, so it fails with not-found for path before the
// brace scan could reach a later resource.
func resourceBodyAt(
	dec *jsontext.Decoder, deployment []byte, index int, path string,
) ([]byte, error) {
	at, err := streamArrayAt(dec, index)
	if err != nil {
		return nil, err
	}

	if !at || dec.PeekKind() != jsontext.KindBeginObject {
		return nil, notFoundCipher(path)
	}

	obj, err := objectAt(dec, deployment)
	if err != nil {
		return nil, err
	}

	return deployment[obj:], nil
}

// objectAt returns the offset in data of the object that dec streams next.
// Only white space and one separator stand between the last token dec read
// and that object, so the first brace is its opening brace.
func objectAt(dec *jsontext.Decoder, data []byte) (int, error) {
	start := int(dec.InputOffset())

	open := bytes.IndexByte(data[start:], '{')
	if open < 0 {
		return 0, decodeDeploymentFailed(errUnexpectedValue)
	}

	return start + open, nil
}

// streamResourceSecret reads the resource in body in two passes. The first
// pass finds its URN, as the masked read does. The second walks to the secret
// and reads the rest of the resource, so duplicate keys in it fail the lookup.
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
// whether it holds a non-empty string URN. It stops at the URN.
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

// streamSectionSecret walks the resource object that dec streams to
// the secret at section/rest. Once the section answers, the rest of the
// resource is read so that the decoder checks it.
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

	cipher, found, err := seekMember(dec, section, func() ([]byte, error) {
		return streamDescend(dec, rest, path)
	})
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, notFoundCipher(path)
	}

	if err := drainStreamedObject(dec); err != nil {
		return nil, err
	}

	return cipher, nil
}

// streamedURNPresent reads the streamed URN value that dec streams and reports
// whether it is a non-empty string. Any other value is skipped, so the
// resource reads as one without a URN, as in the masked read.
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
// rest and returns the ciphertext bytes when it is a secret. A secret masks
// only itself, so an object that is a secret answers not-found for any path
// below it, as maskValue stops at a secret.
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

// streamDescendObject walks the streamed object that dec streams to the value
// at rest. It reads each member of the object once: the member at rest[0]
// descends, the signature member records whether the object is a secret, and
// every other member is skipped. The object answers only after it ends, so a
// signed object answers not-found, and the cost of the walk is the size of
// the document, whatever the depth of the path.
func streamDescendObject(dec *jsontext.Decoder, rest []string, path string) ([]byte, error) {
	if _, err := dec.ReadToken(); err != nil {
		return nil, decodeDeploymentFailed(err)
	}

	walk := objectWalk{rest: rest, path: path, cipher: nil, descended: false, descendErr: nil, signed: false}

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return nil, err
		}

		if end {
			break
		}

		if err := walk.visit(dec, name); err != nil {
			return nil, err
		}
	}

	if !walk.descended || walk.signed {
		return nil, notFoundCipher(path)
	}

	return walk.cipher, walk.descendErr
}

// objectWalk is the state of the walk of one object on the path: the result
// of the descent and whether the object carries the secret signature.
type objectWalk struct {
	rest       []string
	path       string
	cipher     []byte
	descended  bool
	descendErr error
	signed     bool
}

// visit reads the streamed value of the member named name. It returns an
// error that ends the walk.
func (w *objectWalk) visit(dec *jsontext.Decoder, name string) error {
	switch {
	case name == w.rest[0] && !w.descended:
		w.descended = true
		w.cipher, w.descendErr = streamDescend(dec, w.rest[1:], w.path)

		// A secret without a usable ciphertext leaves the decoder after its
		// object, so the walk goes on and the signature decides.
		if w.descendErr != nil && !errors.Is(w.descendErr, ErrInvalidUpdate) {
			return w.descendErr
		}

		return nil
	case name == secretSigKey:
		text, err := streamedText(dec)
		if err != nil {
			return err
		}

		w.signed = w.signed || text == secretSigValue

		return nil
	default:
		return skipStreamed(dec)
	}
}

// streamDescendArray walks the streamed array that dec streams to element
// rest[0], then to the value below it. The index is a canonical decimal.
func streamDescendArray(dec *jsontext.Decoder, rest []string, path string) ([]byte, error) {
	index, ok := parseIndex(rest[0])
	if !ok {
		return nil, notFoundCipher(path)
	}

	at, err := streamArrayAt(dec, index)
	if err != nil {
		return nil, err
	}

	if !at {
		return nil, notFoundCipher(path)
	}

	cipher, err := streamDescend(dec, rest[1:], path)
	if err != nil {
		return nil, err
	}

	if err := drainStreamedArray(dec); err != nil {
		return nil, err
	}

	return cipher, nil
}

// streamTargetSecret reads the streamed value that dec streams at the path. A
// value that is not an object is not a secret and is not read. An object is
// read member by member: only its signature and ciphertext fields are kept.
// A value that is not a secret fails with not-found for the path, so a caller
// cannot probe paths.
func streamTargetSecret(dec *jsontext.Decoder, path string) ([]byte, error) {
	if dec.PeekKind() != jsontext.KindBeginObject {
		return nil, notFoundCipher(path)
	}

	fields, err := scanSecretObject(dec)
	if err != nil {
		return nil, err
	}

	if !fields.signed {
		return nil, notFoundCipher(path)
	}

	raw := fields.cipher
	if raw == "" {
		raw = fields.innerCipher
	}

	if raw == "" {
		return nil, fmt.Errorf("secret holds no ciphertext: %w", ErrInvalidUpdate)
	}

	cipher, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("secret holds no decodable ciphertext: %w", ErrInvalidUpdate)
	}

	return cipher, nil
}

// secretFields is what a streamed object shows of a secret: whether its
// signature is the service signature, its ciphertext field, and the ciphertext
// field of its value object in newer checkpoints. A field that is not a
// string reads as empty.
type secretFields struct {
	signed      bool
	cipher      string
	innerCipher string
}

// scanSecretObject streams the object that dec streams. It keeps the secret
// fields and skips every other member. Every member streams, so the decoder
// checks duplicate keys inside the object.
func scanSecretObject(dec *jsontext.Decoder) (secretFields, error) {
	var fields secretFields

	open, err := openStreamedObject(dec)
	if err != nil || !open {
		return fields, err
	}

	for {
		name, end, err := streamedMember(dec)
		if err != nil || end {
			return fields, err
		}

		if err := scanSecretMember(dec, name, &fields); err != nil {
			return fields, err
		}
	}
}

// scanSecretMember reads the streamed member value named name of a secret
// object into fields. Only the signature, the ciphertext and the value object
// keep anything; every other value is skipped.
func scanSecretMember(dec *jsontext.Decoder, name string, fields *secretFields) error {
	var err error

	switch name {
	case secretSigKey:
		var text string

		text, err = streamedText(dec)
		fields.signed = text == secretSigValue
	case ciphertextField:
		fields.cipher, err = streamedText(dec)
	case "value":
		if dec.PeekKind() != jsontext.KindBeginObject {
			return skipStreamed(dec)
		}

		fields.innerCipher, err = scanCiphertextObject(dec)
	default:
		return skipStreamed(dec)
	}

	return err
}

// scanCiphertextObject streams the object that dec streams and returns its
// ciphertext field. Every other member is skipped.
func scanCiphertextObject(dec *jsontext.Decoder) (string, error) {
	open, err := openStreamedObject(dec)
	if err != nil || !open {
		return "", err
	}

	var cipher string

	for {
		name, end, err := streamedMember(dec)
		if err != nil || end {
			return cipher, err
		}

		if name != ciphertextField {
			if skipErr := skipStreamed(dec); skipErr != nil {
				return "", skipErr
			}

			continue
		}

		cipher, err = streamedText(dec)
		if err != nil {
			return "", err
		}
	}
}

// streamedText reads the streamed value that dec streams and returns its text.
// A value that is not a string reads as empty, and is skipped.
func streamedText(dec *jsontext.Decoder) (string, error) {
	if dec.PeekKind() != jsontext.KindString {
		return "", skipStreamed(dec)
	}

	tok, err := dec.ReadToken()
	if err != nil {
		return "", decodeDeploymentFailed(err)
	}

	return tok.String(), nil
}

// parseIndex parses an array index of a reveal path. Only a canonical decimal
// is an index: no sign, no leading zero, and only digits.
func parseIndex(token string) (int, bool) {
	if token == "" || (token != "0" && token[0] == '0') {
		return 0, false
	}

	for _, r := range token {
		if r < '0' || r > '9' {
			return 0, false
		}
	}

	index, err := strconv.Atoi(token)
	if err != nil {
		return 0, false
	}

	return index, true
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
