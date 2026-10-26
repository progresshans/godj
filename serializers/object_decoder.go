package serializers

import "fmt"

const maximumObjectDecoderNodes = 4096

// ObjectDecoder is an immutable declaration of object envelopes and their
// leaf Specs. It controls where JSONField may contain arbitrary member names;
// it does not bind values, apply defaults, grant model authority or write rows.
// One decoder budget covers the entire tree, before JSON payload encapsulation.
// Its zero value is invalid.
type ObjectDecoder struct{ node *objectDecoderNode }

type objectDecoderNode struct {
	jsonFields map[string]bool
	children   map[string]*objectDecoderNode
	depth      int
	nodes      int
}

// NewObjectDecoder snapshots the decoding grammar of an existing Spec. The
// caller must still bind decoded objects through that exact Spec or Body.
func NewObjectDecoder(spec Spec) (ObjectDecoder, error) {
	if !spec.valid {
		return ObjectDecoder{}, invalidConfig("spec", "serializer spec is zero or invalid")
	}
	return ObjectDecoder{node: &objectDecoderNode{jsonFields: spec.jsonFieldNames(), depth: 1, nodes: 1}}, nil
}

// ObjectDecoderField declares a named child object. Its name uses the same
// identifier grammar as Spec fields. Requiredness and child value validation
// belong to the prepared input binding, not this syntax decoder.
type ObjectDecoderField struct {
	Name    string
	Decoder ObjectDecoder
}

// ComposeObjectDecoder declares a closed set of child object grammars. Unknown
// members are still decoded with ordinary JSON grammar so the binder can report
// ordered unknown-field errors. Shared children remain immutable and count at
// every occurrence toward the 4096-node and 64-object-depth construction bounds.
func ComposeObjectDecoder(fields ...ObjectDecoderField) (ObjectDecoder, error) {
	if len(fields) == 0 || len(fields) >= maximumObjectDecoderNodes {
		return ObjectDecoder{}, invalidConfig("objects", "object envelope must have a bounded nonempty declaration")
	}
	node := &objectDecoderNode{children: make(map[string]*objectDecoderNode, len(fields)), depth: 1, nodes: 1}
	for index, field := range fields {
		child := field.Decoder.node
		_, duplicate := node.children[field.Name]
		if !validFieldName(field.Name) || duplicate || child == nil {
			return ObjectDecoder{}, invalidConfig(fmt.Sprintf("objects[%d]", index), "object name or decoder is invalid or duplicated")
		}
		if child.nodes > maximumObjectDecoderNodes-node.nodes || child.depth >= hardMaxDepth {
			return ObjectDecoder{}, invalidConfig("objects", "object declaration exceeds its node or depth bound")
		}
		node.nodes += child.nodes
		if child.depth+1 > node.depth {
			node.depth = child.depth + 1
		}
		node.children[field.Name] = child
	}
	return ObjectDecoder{node: node}, nil
}

func (decoder ObjectDecoder) Valid() bool { return decoder.node != nil }

func (decoder ObjectDecoder) DecodeObject(document []byte, limits Limits) (Object, error) {
	if !decoder.Valid() {
		return Object{}, invalidConfig("objects", "object decoder is zero or unprepared")
	}
	value, err := decodeObjectDocument(document, limits, decoder.node, false, false)
	if err != nil {
		return Object{}, err
	}
	object, ok := value.AsObject()
	if !ok {
		return Object{}, invalidDocument("document", "top-level JSON value must be an object", nil)
	}
	return object, nil
}

// DecodeList applies this object grammar independently to every root-array
// item while retaining one aggregate request budget, including nested objects.
func (decoder ObjectDecoder) DecodeList(document []byte, limits Limits) ([]Object, error) {
	if !decoder.Valid() {
		return nil, invalidConfig("objects", "object decoder is zero or unprepared")
	}
	value, err := decodeObjectDocument(document, limits, decoder.node, false, true)
	if err != nil {
		return nil, err
	}
	return decodedObjectList(value)
}
