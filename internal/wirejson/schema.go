package wirejson

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// These streaming helpers consume closed schema members after Scan has checked
// framing and global resource limits. Schema owners choose required fields and
// type-specific array/string bounds before allocating their typed result.
func Object(decoder *json.Decoder, required []string, handlers map[string]func() error) error {
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

func Array(decoder *json.Decoder, maximum int, parseElement func(int) error) error {
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

func Bool(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if _, ok := token.(bool); !ok {
		return errors.New("expected JSON boolean")
	}
	return nil
}

func UintToken(decoder *json.Decoder) (uint64, error) {
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

func IntToken(decoder *json.Decoder) (int64, error) {
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

func String(decoder *json.Decoder, maximum int) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	value, ok := token.(string)
	if !ok {
		return "", errors.New("expected JSON string")
	}
	if maximum < 0 || len(value) > maximum || !utf8.ValidString(value) {
		return "", ErrResource
	}
	return value, nil
}
