package clientgen

import (
	"regexp"
	"slices"
	"strconv"

	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// successStatus matches the key of a success answer.
var successStatus = regexp.MustCompile(`^(2\d\d|2XX)$`)

// The names of the parts of a paged list.
const (
	pageSizeName  = "pageSize"
	pageTokenName = "pageToken"
	pageNextName  = "nextPageToken"
)

// operationBody reads the request body of an operation.
func (st *parseState) operationBody(ptr string, o object, op *model.Operation) error {
	raw, ok := o["requestBody"]
	if !ok {
		return nil
	}

	bptr := ptrJoin(ptr, "requestBody")

	rb, err := asObject(bptr, raw)
	if err != nil {
		return err
	}

	err = checkKeys(bptr, rb, "content", "required")
	if err != nil {
		return err
	}

	body := new(model.Body)

	if body.Required, err = optionalBool(rb, bptr, "required"); err != nil {
		return err
	}

	media, schema, err := st.singleContent(bptr, rb, model.MediaJSON, model.MediaMergePatch)
	if err != nil {
		return err
	}

	body.MediaType = media

	if body.Type, err = st.typeOf(schemaPtr(bptr, media), schema); err != nil {
		return err
	}

	op.Body = body

	return nil
}

// schemaPtr is the JSON pointer of the schema of a media type in a content map.
func schemaPtr(ptr string, media model.MediaType) string {
	return ptrJoin(ptrJoin(ptrJoin(ptr, "content"), string(media)), "schema")
}

// singleContent returns the one media type of a content map, which must be one
// of the allowed ones, and its schema.
func (st *parseState) singleContent(ptr string, o object, allowed ...model.MediaType) (model.MediaType, any, error) {
	cptr := ptrJoin(ptr, "content")

	content, err := objectAt(o, ptr, "content")
	if err != nil {
		return "", nil, err
	}

	keys := sortedKeys(content)
	if len(keys) != 1 {
		return "", nil, unsupported(cptr, "content with more or less than one media type")
	}

	media := model.MediaType(keys[0])

	var found bool

	for _, a := range allowed {
		found = found || a == media
	}

	if !found {
		return "", nil, unsupported(ptrJoin(cptr, keys[0]), "media type")
	}

	mptr := ptrJoin(cptr, keys[0])

	mt, err := asObject(mptr, content[keys[0]])
	if err != nil {
		return "", nil, err
	}

	if err := checkKeys(mptr, mt, "schema"); err != nil {
		return "", nil, err
	}

	schema, ok := mt["schema"]
	if !ok {
		return "", nil, invalid(ptrJoin(mptr, "schema"), "the key is needed")
	}

	return media, schema, nil
}

// operationResult reads the responses: one success answer and the default
// error answer.
func (st *parseState) operationResult(ptr string, o object, op *model.Operation) error {
	rptr := ptrJoin(ptr, "responses")

	responses, err := objectAt(o, ptr, "responses")
	if err != nil {
		return err
	}

	var success string

	for _, key := range sortedKeys(responses) {
		switch {
		case key == "default":
		case successStatus.MatchString(key) && success == "":
			success = key
		default:
			return unsupported(ptrJoin(rptr, key), "response status")
		}
	}

	if success == "" {
		return invalid(rptr, "the operation needs a success answer")
	}

	if _, ok := responses["default"]; !ok {
		return invalid(rptr, "the operation needs the default error answer")
	}

	if err := st.checkErrorResponse(ptrJoin(rptr, "default"), responses["default"]); err != nil {
		return err
	}

	return st.successResponse(ptrJoin(rptr, success), success, responses[success], op)
}

// checkErrorResponse checks that the default answer is the error schema.
func (st *parseState) checkErrorResponse(ptr string, node any) error {
	o, err := asObject(ptr, node)
	if err != nil {
		return err
	}

	err = checkKeys(ptr, o, "description", "content")
	if err != nil {
		return err
	}

	_, schema, err := st.singleContent(ptr, o, model.MediaJSON)
	if err != nil {
		return err
	}

	ref, isObject := schema.(object)
	if !isObject || len(ref) != 1 || ref["$ref"] != schemaRefPrefix+errorSchemaName {
		return invalid(ptr, "the default answer must be the schema "+errorSchemaName)
	}

	return nil
}

// successResponse reads the success answer.
func (st *parseState) successResponse(ptr, key string, node any, op *model.Operation) error {
	o, err := asObject(ptr, node)
	if err != nil {
		return err
	}

	err = checkKeys(ptr, o, "description", "content", "headers")
	if err != nil {
		return err
	}

	if key != "2XX" {
		op.Result.Status, _ = strconv.Atoi(key) //nolint:errcheck // The key matched three digits.
	}

	if op.Result.ETag, err = readHeaders(ptr, o); err != nil {
		return err
	}

	if _, ok := o["content"]; !ok {
		return nil
	}

	_, schema, err := st.singleContent(ptr, o, model.MediaJSON)
	if err != nil {
		return err
	}

	t, err := st.typeOf(schemaPtr(ptr, model.MediaJSON), schema)
	if err != nil {
		return err
	}

	op.Result.Type = &t

	return nil
}

// readHeaders reads the headers of an answer. Only ETag is in the subset. It
// returns whether the answer has the ETag header.
func readHeaders(ptr string, o object) (bool, error) {
	headers, err := optionalObjectAt(o, ptr, "headers")
	if err != nil {
		return false, err
	}

	for _, name := range sortedKeys(headers) {
		if name != "ETag" {
			return false, unsupported(ptrJoin(ptrJoin(ptr, "headers"), name), "response header")
		}

		hptr := ptrJoin(ptrJoin(ptr, "headers"), name)

		h, hErr := asObject(hptr, headers[name])
		if hErr != nil {
			return false, hErr
		}

		if ref, isRef := h["$ref"]; isRef && (len(h) != 1 || ref != "#/components/headers/ETag") {
			return false, unsupported(hptr, "header $ref")
		}
	}

	return len(headers) > 0, nil
}

// checkPage sets Page for a list that pages with a cursor. It returns an
// error when only a part of the three names is there.
func checkPage(ptr string, op *model.Operation, schemas map[string]*model.Schema) error {
	hasSize, hasToken := hasQuery(op, pageSizeName), hasQuery(op, pageTokenName)
	hasNext, items := answerParts(op, schemas)

	switch {
	case !hasSize && !hasToken && !hasNext:
		return nil
	case !hasSize || !hasToken || !hasNext || len(items) != 1:
		return invalid(
			ptr,
			"a list needs pageSize, pageToken, nextPageToken and one array in the answer, or none of them",
		)
	}

	op.Page = &model.Page{
		SizeParameter: pageSizeName, TokenParameter: pageTokenName, ItemsField: items[0], NextField: pageNextName,
	}

	return nil
}

// hasQuery says that an operation has a query parameter with the name.
func hasQuery(op *model.Operation, name string) bool {
	return slices.ContainsFunc(op.Parameters, func(p model.Parameter) bool {
		return p.In == model.InQuery && p.Name == name
	})
}

// answerParts says whether the answer of an operation has the field for the
// next cursor, and returns the names of its array fields.
func answerParts(op *model.Operation, schemas map[string]*model.Schema) (bool, []string) {
	t := op.Result.Type
	if t == nil || t.Kind != model.TypeRef || schemas[t.Ref] == nil {
		return false, nil
	}

	var (
		hasNext bool
		items   []string
	)

	for _, f := range schemas[t.Ref].Fields {
		hasNext = hasNext || f.Name == pageNextName

		if f.Type.Kind == model.TypeArray {
			items = append(items, f.Name)
		}
	}

	return hasNext, items
}
