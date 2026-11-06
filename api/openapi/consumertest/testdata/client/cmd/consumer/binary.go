package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
)

// The stored canonical JSON sorts object keys and escapes HTML while retaining
// number tokens. The wire renderer can spell strings differently. Reconstruct
// the expected stored bytes with the standard library, without GoDj imports.
func expectedPayloadDigest(raw []byte) (hs.NilString, error) {
	if string(raw) == "null" {
		value := hs.NilString{}
		value.SetToNull()
		return value, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return hs.NilString{}, fail("binary expected JSON is malformed")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return hs.NilString{}, fail("binary expected JSON cannot be represented")
	}
	sum := sha256.Sum256(canonical)
	return hs.NewNilString(base64.StdEncoding.EncodeToString(sum[:])), nil
}

func checkHelpdeskBinaryDigest(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) error {
	expected, err := expectedPayloadDigest(original.ExternalPayload)
	if err != nil {
		return err
	}
	if original.ExternalPayloadDigest != expected {
		return fail("binary digest differs from stored JSON in generated client")
	}
	for _, kind := range []reflect.Type{reflect.TypeOf(hs.TicketCreate{}), reflect.TypeOf(hs.TicketUpdate{}), reflect.TypeOf(hs.TicketPatch{})} {
		if _, exists := kind.FieldByName("ExternalPayloadDigest"); exists {
			return fail("read-only binary became generated request member")
		}
	}
	response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: original.ID})
	stored, ok := response.(*hs.Ticket)
	if err != nil || !ok || transport.lastStatus() != http.StatusOK || !sameHelpdeskTicket(*stored, original) {
		return fail("binary no-op round trip changed bytes or presence")
	}
	if !stored.ExternalPayloadDigest.Null {
		bytes, err := base64.StdEncoding.Strict().DecodeString(stored.ExternalPayloadDigest.Value)
		if err != nil || len(bytes) != 32 {
			return fail("generated digest is not canonical SHA-256 bytes")
		}
	}
	return nil
}

func checkGeneratedBinaryWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"external_url":null,"external_payload":null,"external_reference":null,"expected_cost":null,"effort":null,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_on":null,"service_at":null,"labels":[]`
	for _, test := range []struct {
		raw   string
		null  bool
		value string
	}{
		{`null`, true, ""}, {`""`, false, ""}, {`"AP9hgA=="`, false, "AP9hgA=="}, {`"Zg=="`, false, "Zg=="},
		// Output accepts existing values beyond the current model input length.
		{`"` + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 33))) + `"`, false, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 33)))},
	} {
		calls := 0
		client, err := nullableBooleanWireClient(base+`,"external_payload_digest":`+test.raw+`}`, `{}`, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.ExternalPayloadDigest.Null != test.null || !test.null && row.ExternalPayloadDigest.Value != test.value {
			return fail("binary generated decoder changed value or null")
		}
	}
	for _, suffix := range []string{`}`, `,"external_payload_digest":false}`, `,"external_payload_digest":0}`, `,"external_payload_digest":[]}`, `,"external_payload_digest":{}}`, `,"external_payload_digest":"AP8"}`, `,"external_payload_digest":"AP8==="}`, `,"external_payload_digest":"AP8_"}`, `,"external_payload_digest":" AP8="}`, `,"external_payload_digest":"AP8=\n"}`, `,"external_payload_digest":"` + strings.Repeat("AAAA", (1<<20)/3+2) + `"}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1}); err == nil || calls != 1 {
			return fail("binary generated decoder admitted missing, malformed or oversized response")
		}
	}
	return nil
}
