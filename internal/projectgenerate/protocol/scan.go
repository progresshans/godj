package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/schema/ir"
)

const maximumObjectMembers = 16

type wireBudget struct {
	fields uint64
	nodes  uint64
}

func scanJSONDocument(document []byte, maximum int) error {
	if err := validateDocumentSize(len(document), maximum); err != nil {
		return err
	}
	if len(document) == 0 || !utf8.Valid(document) || bytes.HasPrefix(document, []byte{0xef, 0xbb, 0xbf}) {
		return errors.New("invalid UTF-8 JSON document")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if value, ok := token.(string); ok {
		return validateWireString("JSON string", value)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	depth++
	if depth > MaxJSONDepth {
		return fmt.Errorf("JSON depth exceeds %d", MaxJSONDepth)
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		members := 0
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("non-string JSON object key")
			}
			if err := validateWireString("JSON object key", key); err != nil {
				return err
			}
			members++
			if members > maximumObjectMembers {
				return fmt.Errorf("JSON object members exceed %d", maximumObjectMembers)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, depth); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid JSON object ending")
		}
	case '[':
		entries := 0
		for decoder.More() {
			entries++
			if entries > MaxApps {
				return fmt.Errorf("JSON array entries exceed %d", MaxApps)
			}
			if err := scanJSONValue(decoder, depth); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid JSON array ending")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func preflightResponse(document []byte) (string, uint64, error) {
	if err := scanJSONDocument(document, MaxResponseBytes); err != nil {
		return "", 0, err
	}
	decoder := newWireDecoder(document)
	var status string
	var version uint64
	var hasProjectSpec, hasFailure bool
	err := parseObject(decoder, []string{"protocol_version", "status"}, map[string]func() error{
		"protocol_version": func() error {
			value, err := parseUint(decoder)
			version = value
			return err
		},
		"status": func() error {
			value, err := parseString(decoder)
			status = value
			return err
		},
		"project_spec": func() error {
			hasProjectSpec = true
			return parseProjectSpec(decoder)
		},
		"error": func() error {
			hasFailure = true
			return parseFailure(decoder)
		},
	})
	if err != nil {
		return "", 0, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", 0, errors.New("trailing response value")
	}
	if version > 65_535 {
		return "", 0, errors.New("protocol version exceeds bound")
	}
	switch status {
	case "ok":
		if !hasProjectSpec || hasFailure {
			return "", 0, errors.New("invalid success response shape")
		}
	case "error":
		if hasProjectSpec || !hasFailure {
			return "", 0, errors.New("invalid error response shape")
		}
	default:
		return "", 0, errors.New("invalid response status")
	}
	return status, version, nil
}

func preflightRequest(document []byte) (uint64, string, error) {
	decoder := newWireDecoder(document)
	var version uint64
	var command string
	err := parseObject(decoder, []string{"protocol_version", "command"}, map[string]func() error{
		"protocol_version": func() error {
			value, err := parseUint(decoder)
			version = value
			return err
		},
		"command": func() error {
			value, err := parseString(decoder)
			command = value
			return err
		},
	})
	if err != nil {
		return 0, "", err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return 0, "", errors.New("trailing request value")
	}
	if version > 65_535 {
		return 0, "", errors.New("protocol version exceeds bound")
	}
	return version, command, nil
}

func parseProjectSpec(decoder *json.Decoder) error {
	budget := &wireBudget{}
	return parseObject(decoder, []string{"project", "apps"}, map[string]func() error{
		"project": func() error { return parsePackage(decoder) },
		"apps": func() error {
			return parseArray(decoder, MaxApps, func(index int) error {
				if err := budget.consumeNodes(1); err != nil {
					return err
				}
				return parseApp(decoder, budget)
			})
		},
	})
}

func parsePackage(decoder *json.Decoder) error {
	return parseObject(decoder, []string{"package_name", "import_path", "directory"}, map[string]func() error{
		"package_name": func() error { _, err := parseString(decoder); return err },
		"import_path":  func() error { _, err := parseString(decoder); return err },
		"directory":    func() error { _, err := parseString(decoder); return err },
	})
}

func parseApp(decoder *json.Decoder, budget *wireBudget) error {
	return parseObject(decoder, []string{"alias", "package", "schema"}, map[string]func() error{
		"alias":   func() error { _, err := parseString(decoder); return err },
		"package": func() error { return parsePackage(decoder) },
		"schema":  func() error { return parseSchema(decoder, budget) },
	})
}

func parseSchema(decoder *json.Decoder, budget *wireBudget) error {
	return parseObject(decoder, []string{"format_version", "app_label", "models"}, map[string]func() error{
		"format_version": func() error {
			version, err := parseUint(decoder)
			if err == nil && version != uint64(ir.CurrentFormatVersion) {
				return fmt.Errorf("schema format version %d is incompatible", version)
			}
			return err
		},
		"app_label": func() error { _, err := parseString(decoder); return err },
		"models": func() error {
			return parseArray(decoder, projectspec.MaxModelsPerApp, func(int) error {
				if err := budget.consumeNodes(1); err != nil {
					return err
				}
				return parseModel(decoder, budget)
			})
		},
	})
}

func parseModel(decoder *json.Decoder, budget *wireBudget) error {
	return parseObject(decoder, []string{"name", "go_name", "db_table", "fields"}, map[string]func() error{
		"name":     func() error { _, err := parseString(decoder); return err },
		"go_name":  func() error { _, err := parseString(decoder); return err },
		"db_table": func() error { _, err := parseString(decoder); return err },
		"fields": func() error {
			return parseArray(decoder, projectspec.MaxFieldsPerModel, func(int) error {
				if err := budget.consumeFields(1); err != nil {
					return err
				}
				if err := budget.consumeNodes(1); err != nil {
					return err
				}
				return parseField(decoder, budget)
			})
		},
	})
}

func parseField(decoder *json.Decoder, budget *wireBudget) error {
	required := []string{"name", "go_name", "column", "kind", "primary_key", "nullable"}
	return parseObject(decoder, required, map[string]func() error{
		"name":        func() error { _, err := parseString(decoder); return err },
		"go_name":     func() error { _, err := parseString(decoder); return err },
		"column":      func() error { _, err := parseString(decoder); return err },
		"kind":        func() error { _, err := parseString(decoder); return err },
		"primary_key": func() error { return parseBool(decoder) },
		"nullable":    func() error { return parseBool(decoder) },
		"max_length":  func() error { _, err := parseInt(decoder); return err },
		"default": func() error {
			if err := budget.consumeNodes(1); err != nil {
				return err
			}
			return parseDefault(decoder)
		},
		"relation": func() error {
			if err := budget.consumeNodes(3); err != nil {
				return err
			}
			return parseRelation(decoder)
		},
	})
}

func parseDefault(decoder *json.Decoder) error {
	return parseObject(decoder, []string{"kind"}, map[string]func() error{
		"kind":    func() error { _, err := parseString(decoder); return err },
		"string":  func() error { _, err := parseString(decoder); return err },
		"boolean": func() error { return parseBool(decoder) },
		"integer": func() error { _, err := parseInt(decoder); return err },
	})
}

func parseRelation(decoder *json.Decoder) error {
	return parseObject(decoder, []string{"target", "cardinality", "reverse", "on_delete"}, map[string]func() error{
		"target": func() error {
			return parseObject(decoder, []string{"app_label", "model_name"}, map[string]func() error{
				"app_label":  func() error { _, err := parseString(decoder); return err },
				"model_name": func() error { _, err := parseString(decoder); return err },
			})
		},
		"cardinality": func() error { _, err := parseString(decoder); return err },
		"reverse": func() error {
			return parseObject(decoder, nil, map[string]func() error{
				"name":     func() error { _, err := parseString(decoder); return err },
				"disabled": func() error { return parseBool(decoder) },
			})
		},
		"on_delete": func() error { _, err := parseString(decoder); return err },
	})
}

func parseFailure(decoder *json.Decoder) error {
	return parseObject(decoder, []string{"category", "code"}, map[string]func() error{
		"category": func() error { _, err := parseString(decoder); return err },
		"code":     func() error { _, err := parseString(decoder); return err },
	})
}

func parseObject(decoder *json.Decoder, required []string, handlers map[string]func() error) error {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("expected JSON object")
	}
	seen := make(map[string]struct{}, len(handlers))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("expected JSON object key")
		}
		handler, allowed := handlers[key]
		if !allowed {
			return fmt.Errorf("unknown JSON member %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate JSON member %q", key)
		}
		seen[key] = struct{}{}
		if err := handler(); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("invalid JSON object ending")
	}
	for _, key := range required {
		if _, exists := seen[key]; !exists {
			return fmt.Errorf("missing JSON member %q", key)
		}
	}
	return nil
}

func parseArray(decoder *json.Decoder, maximum int, parseElement func(int) error) error {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return errors.New("expected JSON array")
	}
	count := 0
	for decoder.More() {
		if count >= maximum {
			return fmt.Errorf("JSON array entries exceed %d", maximum)
		}
		if err := parseElement(count); err != nil {
			return err
		}
		count++
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return errors.New("invalid JSON array ending")
	}
	return nil
}

func parseString(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	value, ok := token.(string)
	if !ok {
		return "", errors.New("expected JSON string")
	}
	if err := validateWireString("JSON string", value); err != nil {
		return "", err
	}
	return value, nil
}

func parseBool(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if _, ok := token.(bool); !ok {
		return errors.New("expected JSON boolean")
	}
	return nil
}

func parseUint(decoder *json.Decoder) (uint64, error) {
	token, err := decoder.Token()
	if err != nil {
		return 0, err
	}
	number, ok := token.(json.Number)
	if !ok {
		return 0, errors.New("expected unsigned JSON integer")
	}
	value, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return 0, errors.New("expected canonical unsigned JSON integer")
	}
	return value, nil
}

func parseInt(decoder *json.Decoder) (int64, error) {
	token, err := decoder.Token()
	if err != nil {
		return 0, err
	}
	number, ok := token.(json.Number)
	if !ok {
		return 0, errors.New("expected signed JSON integer")
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return 0, errors.New("expected canonical signed JSON integer")
	}
	return value, nil
}

func newWireDecoder(document []byte) *json.Decoder {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	return decoder
}

func (budget *wireBudget) consumeFields(count uint64) error {
	if budget.fields > projectspec.MaxAggregateFields || count > projectspec.MaxAggregateFields-budget.fields {
		return fmt.Errorf("aggregate schema fields exceed %d", projectspec.MaxAggregateFields)
	}
	budget.fields += count
	return nil
}

func (budget *wireBudget) consumeNodes(count uint64) error {
	if budget.nodes > projectspec.MaxAggregateNodes || count > projectspec.MaxAggregateNodes-budget.nodes {
		return fmt.Errorf("aggregate schema nodes exceed %d", projectspec.MaxAggregateNodes)
	}
	budget.nodes += count
	return nil
}
