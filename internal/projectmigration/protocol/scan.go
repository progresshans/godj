package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/migrations/definition"
)

const (
	maximumObjectMembers = 16
	maximumJSONValues    = 4_000_000
)

var maximumJSONStringBytes = base64.StdEncoding.EncodedLen(definition.MaxDocumentBytes)

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
	version, ok := wirejson.ParseUint(string(object["protocol_version"]), 65_535)
	if !ok {
		return "", 0, errors.New("invalid protocol version")
	}
	var status string
	if err := json.Unmarshal(object["status"], &status); err != nil {
		return "", 0, errors.New("invalid response status")
	}
	switch status {
	case "ok":
		if !wirejson.HasKeys(object, "protocol_version", "status", "result") {
			return "", 0, errors.New("invalid success response shape")
		}
	case "error":
		if !wirejson.HasKeys(object, "protocol_version", "status", "error") {
			return "", 0, errors.New("invalid error response shape")
		}
	default:
		return "", 0, errors.New("invalid response status")
	}
	return status, version, nil
}

func scanJSONDocument(document []byte, maximum int) error {
	return wirejson.Scan(document, wirejson.Limits{
		Bytes: maximum, Containers: MaxJSONDepth, Values: maximumJSONValues,
		ObjectKeys: maximumObjectMembers, ArrayValues: MaxProjectApps,
		StringBytes: maximumJSONStringBytes, KeyBytes: 256, RejectNull: true,
	})
}
