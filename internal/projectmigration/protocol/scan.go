package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations/definition"
)

const (
	maximumObjectMembers = 16
	maximumJSONValues    = 4_000_000
)

var maximumJSONStringBytes = base64.StdEncoding.EncodedLen(definition.MaxDocumentBytes)

type scanBudget struct {
	values int
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
	budget := &scanBudget{}
	if err := scanJSONValue(decoder, budget, 0); err != nil {
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

func scanJSONValue(decoder *json.Decoder, budget *scanBudget, depth int) error {
	budget.values++
	if budget.values > maximumJSONValues {
		return fmt.Errorf("JSON values exceed %d", maximumJSONValues)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("JSON null is not permitted")
	}
	if value, ok := token.(string); ok {
		if !utf8.ValidString(value) || len(value) > maximumJSONStringBytes {
			return fmt.Errorf("JSON string exceeds %d decoded bytes", maximumJSONStringBytes)
		}
		return nil
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
			if !ok || !utf8.ValidString(key) || len(key) > 256 {
				return errors.New("invalid JSON object key")
			}
			members++
			if members > maximumObjectMembers {
				return fmt.Errorf("JSON object members exceed %d", maximumObjectMembers)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, budget, depth); err != nil {
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
			if entries > MaxProjectApps {
				return fmt.Errorf("JSON array entries exceed %d", MaxProjectApps)
			}
			if err := scanJSONValue(decoder, budget, depth); err != nil {
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
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return "", 0, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", 0, errors.New("trailing response value")
	}
	version, ok := canonicalRawUint(object["protocol_version"], 65_535)
	if !ok {
		return "", 0, errors.New("invalid protocol version")
	}
	var status string
	if err := json.Unmarshal(object["status"], &status); err != nil {
		return "", 0, errors.New("invalid response status")
	}
	switch status {
	case "ok":
		if !hasExactRawKeys(object, "protocol_version", "status", "result") {
			return "", 0, errors.New("invalid success response shape")
		}
	case "error":
		if !hasExactRawKeys(object, "protocol_version", "status", "error") {
			return "", 0, errors.New("invalid error response shape")
		}
	default:
		return "", 0, errors.New("invalid response status")
	}
	return status, version, nil
}

func decodeCanonical(document []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, document) {
		return errors.New("non-canonical JSON document")
	}
	return nil
}

func canonicalRawUint(raw json.RawMessage, maximum uint64) (uint64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	text := string(raw)
	if text == "" || (len(text) > 1 && text[0] == '0') {
		return 0, false
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(text, 10, 64)
	return value, err == nil && value <= maximum
}

func hasExactRawKeys(object map[string]json.RawMessage, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			return false
		}
	}
	return true
}
