package systemstate

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"

	"github.com/progresshans/godj/admin"
)

const auditChangedFieldsPrefix = "v1."

func encodeAuditChangedFields(fields []string) (string, error) {
	// Reuse Admin's authoritative semantic validation rather than copying its
	// identifier, duplicate, and item-count rules into the persistence layer.
	if _, err := admin.PrepareEvent(
		"systemstate-codec",
		"systemstate.audit",
		1,
		admin.ActionChange,
		append([]string(nil), fields...),
		"",
	); err != nil {
		return "", &Error{
			Code:   CodeInvalidInput,
			Field:  "audit_changed_fields",
			Detail: "audit changed fields are invalid",
			Cause:  err,
		}
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 2+len(fields)*8))
	_ = binary.Write(buffer, binary.BigEndian, uint16(len(fields)))
	for _, field := range fields {
		_ = binary.Write(buffer, binary.BigEndian, uint16(len(field)))
		_, _ = buffer.WriteString(field)
	}
	payload := auditChangedFieldsPrefix + base64.RawURLEncoding.EncodeToString(buffer.Bytes())
	if len(payload) > auditChangedFieldsMaxLength {
		return "", &Error{
			Code:   CodeInvalidInput,
			Field:  "audit_changed_fields",
			Detail: "audit changed fields exceed the current storage bound",
		}
	}
	return payload, nil
}

func decodeAuditChangedFields(payload string) ([]string, error) {
	corrupt := func(cause error) ([]string, error) {
		return nil, &Error{
			Code:   CodeCorruptState,
			Field:  "audit_changed_fields",
			Detail: "stored audit changed fields are malformed or incompatible",
			Cause:  cause,
		}
	}
	if len(payload) <= len(auditChangedFieldsPrefix) || len(payload) > auditChangedFieldsMaxLength ||
		!strings.HasPrefix(payload, auditChangedFieldsPrefix) {
		return corrupt(nil)
	}
	wire, err := base64.RawURLEncoding.Strict().DecodeString(payload[len(auditChangedFieldsPrefix):])
	if err != nil {
		return corrupt(err)
	}
	reader := bytes.NewReader(wire)
	var count uint16
	if err := binary.Read(reader, binary.BigEndian, &count); err != nil || int(count) > admin.MaximumChangedFields {
		return corrupt(err)
	}
	fields := make([]string, int(count))
	for index := range fields {
		var length uint16
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil ||
			length == 0 || int(length) > admin.MaximumModelBytes || int(length) > reader.Len() {
			return corrupt(err)
		}
		value := make([]byte, int(length))
		if _, err := io.ReadFull(reader, value); err != nil {
			return corrupt(err)
		}
		fields[index] = string(value)
	}
	if reader.Len() != 0 {
		return corrupt(nil)
	}
	canonical, err := encodeAuditChangedFields(fields)
	if err != nil || canonical != payload {
		return corrupt(err)
	}
	return fields, nil
}
