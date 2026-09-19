// Package attestationio provides strict JSON and bounded-file primitives.
// Attestation owners retain their source inventories, schemas and size limits.
package attestationio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

func RejectDuplicateObjectNames(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON token")
		}
		return errors.New("invalid trailing JSON")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("JSON object name is not a string")
			}
			if _, duplicate := seen[name]; duplicate {
				return errors.New("duplicate JSON object name")
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("JSON object has an invalid closing delimiter")
		}
		return nil
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("JSON array has an invalid closing delimiter")
		}
		return nil
	default:
		return fmt.Errorf("JSON has invalid opening delimiter %q", delimiter)
	}
}

func ReadBoundedRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, errors.New("file exceeds its size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) != info.Size() {
		return nil, errors.New("file changed while it was being read")
	}
	return contents, nil
}

func ReadSourceFile(path string, expectedSize int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, expectedSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) != expectedSize {
		return nil, errors.New("source file changed while it was being read")
	}
	return contents, nil
}

// DecodeDocument validates the bounded JSON wire grammar. The caller owns
// schema semantics and the final canonical-byte equality check.
func DecodeDocument(document []byte, maximum int, label string, destination any) error {
	if len(document) == 0 {
		return io.ErrUnexpectedEOF
	}
	if len(document) > maximum {
		return fmt.Errorf("%s exceeds its size limit", label)
	}
	if !utf8.Valid(document) {
		return fmt.Errorf("%s is not valid UTF-8", label)
	}
	if err := RejectDuplicateObjectNames(document); err != nil {
		return fmt.Errorf("%s has invalid or duplicate object names", label)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%s has an invalid JSON shape", label)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s contains a trailing JSON value", label)
		}
		return fmt.Errorf("%s contains invalid trailing data", label)
	}
	return nil
}
