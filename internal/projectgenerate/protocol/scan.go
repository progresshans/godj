package protocol

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/internal/projectwire"
	"github.com/progresshans/godj/internal/wirejson"
)

const maximumObjectMembers = 16

func preflightResponse(document []byte) (string, uint64, error) {
	if err := scanJSONDocument(document, MaxResponseBytes); err != nil {
		return "", 0, err
	}
	decoder := wirejson.NewDecoder(document)
	var status string
	var version uint64
	var hasProjectSpec, hasFailure bool
	err := wirejson.Object(decoder, []string{"protocol_version", "status"}, map[string]func() error{
		"protocol_version": func() error {
			value, err := wirejson.UintToken(decoder)
			version = value
			return err
		},
		"status": func() error {
			value, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes)
			status = value
			return err
		},
		"project_spec": func() error {
			hasProjectSpec = true
			return projectwire.Scan(decoder)
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
	decoder := wirejson.NewDecoder(document)
	var version uint64
	var command string
	err := wirejson.Object(decoder, []string{"protocol_version", "command"}, map[string]func() error{
		"protocol_version": func() error {
			value, err := wirejson.UintToken(decoder)
			version = value
			return err
		},
		"command": func() error {
			value, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes)
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

func parseFailure(decoder *json.Decoder) error {
	return wirejson.Object(decoder, []string{"category", "code"}, map[string]func() error{
		"category": func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"code":     func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
	})
}

func scanJSONDocument(document []byte, maximum int) error {
	return wirejson.Scan(document, wirejson.Limits{
		Bytes: maximum, Containers: MaxJSONDepth, ObjectKeys: maximumObjectMembers,
		ArrayValues: MaxApps, StringBytes: projectspec.MaxSchemaStringBytes, KeyBytes: projectspec.MaxSchemaStringBytes,
	})
}
