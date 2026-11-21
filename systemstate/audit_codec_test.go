package systemstate

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAuditChangedFieldsCodecCanonicalDetachedAndEmpty(t *testing.T) {
	t.Parallel()
	fields := []string{"title", "published", "summary"}
	payload, err := encodeAuditChangedFields(fields)
	if err != nil {
		t.Fatalf("encodeAuditChangedFields(): %v", err)
	}
	fields[0] = "forged"
	decoded, err := decodeAuditChangedFields(payload)
	if err != nil {
		t.Fatalf("decodeAuditChangedFields(): %v", err)
	}
	if !reflect.DeepEqual(decoded, []string{"title", "published", "summary"}) {
		t.Fatalf("decoded = %v", decoded)
	}
	decoded[0] = "mutated"
	again, err := decodeAuditChangedFields(payload)
	if err != nil || !reflect.DeepEqual(again, []string{"title", "published", "summary"}) {
		t.Fatalf("second decode = (%v,%v), want detached canonical fields", again, err)
	}
	if canonical, err := encodeAuditChangedFields(again); err != nil || canonical != payload {
		t.Fatalf("re-encode = (%q,%v), want exact payload", canonical, err)
	}

	empty, err := encodeAuditChangedFields(nil)
	if err != nil {
		t.Fatalf("encodeAuditChangedFields(empty): %v", err)
	}
	if got, err := decodeAuditChangedFields(empty); err != nil || len(got) != 0 {
		t.Fatalf("decodeAuditChangedFields(empty) = (%v,%v)", got, err)
	}
}

func TestAuditChangedFieldsCodecRejectsInvalidUnknownAndMalformed(t *testing.T) {
	t.Parallel()
	valid, err := encodeAuditChangedFields([]string{"title"})
	if err != nil {
		t.Fatalf("encode valid: %v", err)
	}
	wire, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(valid, auditChangedFieldsPrefix))
	if err != nil {
		t.Fatalf("decode valid wire: %v", err)
	}
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unknown version", payload: "v2." + strings.TrimPrefix(valid, auditChangedFieldsPrefix)},
		{name: "invalid base64", payload: auditChangedFieldsPrefix + "***"},
		{name: "truncated", payload: auditChangedFieldsPrefix + base64.RawURLEncoding.EncodeToString(wire[:len(wire)-1])},
		{name: "trailing", payload: auditChangedFieldsPrefix + base64.RawURLEncoding.EncodeToString(append(wire, 0))},
		{name: "oversize", payload: auditChangedFieldsPrefix + strings.Repeat("A", auditChangedFieldsMaxLength)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields, err := decodeAuditChangedFields(test.payload)
			if err == nil || fields != nil {
				t.Fatalf("decodeAuditChangedFields() = (%v,%v), want nil/error", fields, err)
			}
			var classified *Error
			if !errors.As(err, &classified) || classified.Code != CodeCorruptState || classified.Field != "audit_changed_fields" {
				t.Fatalf("error = %#v", err)
			}
			if strings.Contains(err.Error(), test.payload) {
				t.Fatalf("error leaked stored payload: %q", err)
			}
		})
	}

	for _, fields := range [][]string{{"duplicate", "duplicate"}, {"bad.field"}, {""}} {
		if payload, err := encodeAuditChangedFields(fields); err == nil || payload != "" {
			t.Fatalf("encodeAuditChangedFields(%v) = (%q,%v), want empty/error", fields, payload, err)
		}
	}
}
