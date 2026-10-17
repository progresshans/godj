package serializers

// DecodeList accepts exactly one array of objects. Each object uses this
// spec's declared JSON fields; arbitrary names remain forbidden elsewhere.
// One byte, depth, value and container budget covers the complete request,
// including every payload before its immutable JSON representation is formed.
// This method only decodes. Bind each returned object to apply field policy.
func (spec Spec) DecodeList(document []byte, limits Limits) ([]Object, error) {
	if !spec.valid {
		return nil, invalidConfig("spec", "serializer spec is zero or invalid")
	}
	value, err := decodeDocumentAt(document, limits, spec.jsonFieldNames(), false, 2)
	if err != nil {
		return nil, err
	}
	if value.kind != ValueList {
		return nil, invalidDocument("document", "top-level JSON value must be an array of objects", nil)
	}
	result := make([]Object, len(value.list))
	for index, item := range value.list {
		object, valid := item.AsObject()
		if !valid {
			return nil, invalidDocument("document.array", "every array item must be an object", nil)
		}
		result[index] = object
	}
	return result, nil
}
