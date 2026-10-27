package firstaccountwire_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/firstaccountprotocol"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/project/firstaccount"
)

func descriptionFixture(t testing.TB) firstaccount.Description {
	t.Helper()
	description := firstaccount.Description{SourceSHA256: fingerprint, RoleSHA256: fingerprint, PolicySHA256: fingerprint, EvidenceSHA256: fingerprint, Model: "people.Member", Login: "contact", MaxPasswordBytes: 4096, Fields: []firstaccount.Field{
		{Name: "contact", Label: "Contact", Kind: "email", Required: true, MaxBytes: firstaccount.MaxFieldBytes, Choices: []firstaccount.Choice{}},
		{Name: "region", Label: "Region", Kind: "char", Required: true, HasDefault: true, MaxBytes: firstaccount.MaxFieldBytes, Choices: []firstaccount.Choice{{Value: "NZ", Label: "New Zealand"}, {Value: "line\nchoice", Label: "Quoted choice"}}},
	}}
	document, _ := json.Marshal(description)
	digest := sha256.Sum256(append([]byte("godj.first-account-command.v3\x00"), document...))
	description.Fingerprint = hex.EncodeToString(digest[:])
	if err := firstaccount.ValidateDescription(description); err != nil {
		t.Fatal(err)
	}
	return description
}

func TestDescriptionRetainsTypedDeclarationAndRejectsOpenShapes(t *testing.T) {
	description := descriptionFixture(t)
	document, err := firstaccountwire.EncodeDescription(firstaccountwire.DescriptionResponse{Version: firstaccountprotocol.Version, Description: &description})
	if err != nil {
		t.Fatal(err)
	}
	response, err := firstaccountwire.DecodeDescription(document)
	if err != nil || response.Description == nil || response.Description.Fingerprint != description.Fingerprint || response.Description.Fields[1].Choices[1].Value != "line\nchoice" {
		t.Fatal("typed description changed", err)
	}
	for _, bad := range [][]byte{
		append(bytes.Clone(document), '\n'),
		bytes.Replace(document, []byte(`"version":4`), []byte(`"version":4,"version":4`), 1),
		bytes.Replace(document, []byte(`"version":4`), []byte(`"version":4.0`), 1),
		bytes.Replace(document, []byte(`"version":4`), []byte(`"version":1`), 1),
		bytes.Replace(document, []byte(`"required":true`), []byte(`"required":null`), 1),
		bytes.Replace(document, []byte(`"required":true,`), nil, 1),
		bytes.Replace(document, []byte(`"label":"Contact"`), []byte(`"label":"\ud800"`), 1),
		bytes.Replace(document, []byte(`"label":"Contact"`), []byte(`"label":"Changed"`), 1),
		bytes.Replace(document, []byte(`"label":"Contact"`), []byte(`"label":"Contact","unknown":true`), 1),
		bytes.Repeat([]byte{' '}, firstaccountwire.MaxDescriptionResponseBytes+1),
	} {
		if _, err := firstaccountwire.DecodeDescription(bad); err == nil {
			t.Fatal("malformed or altered description accepted")
		}
	}
	for _, code := range []firstaccountwire.Code{firstaccountwire.InvalidConfig, firstaccountwire.Canceled, firstaccountwire.InternalError} {
		encoded, err := firstaccountwire.EncodeDescription(firstaccountwire.DescriptionResponse{Version: firstaccountprotocol.Version, Code: code})
		if err != nil {
			t.Fatal(err)
		}
		if response, err := firstaccountwire.DecodeDescription(encoded); err != nil || response.Description != nil || response.Code != code {
			t.Fatal("closed description failure lost")
		}
	}
}

func TestDescriptionCollectionBoundsAndKindsAreCanonical(t *testing.T) {
	seal := func(description *firstaccount.Description) {
		description.Fingerprint = ""
		document, err := json.Marshal(description)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(append([]byte("godj.first-account-command.v3\x00"), document...))
		description.Fingerprint = hex.EncodeToString(digest[:])
	}
	description := descriptionFixture(t)
	description.Fields = append(description.Fields, firstaccount.Field{Name: "topics", Label: "Topics", Kind: firstaccount.CollectionKind, Required: true, MaxBytes: 63, MaxItems: 3, Choices: []firstaccount.Choice{}})
	seal(&description)
	document, err := firstaccountwire.EncodeDescription(firstaccountwire.DescriptionResponse{Version: firstaccountprotocol.Version, Description: &description})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := firstaccountwire.DecodeDescription(document)
	if err != nil || decoded.Description == nil || decoded.Description.Fields[2].MaxItems != 3 || decoded.Description.Fields[2].Kind != firstaccount.CollectionKind {
		t.Fatal("collection description lost kind/limit", err)
	}
	for _, mutate := range []func(*firstaccount.Field){
		func(f *firstaccount.Field) { f.MaxItems = 0 }, func(f *firstaccount.Field) {
			f.MaxItems = firstaccount.MaxCollectionItems + 1
			f.MaxBytes = 21 * f.MaxItems
		},
		func(f *firstaccount.Field) { f.MaxBytes-- }, func(f *firstaccount.Field) { f.Nullable = true }, func(f *firstaccount.Field) { f.HasDefault = true },
		func(f *firstaccount.Field) { f.Upload = true }, func(f *firstaccount.Field) { f.Kind = "integer" },
		func(f *firstaccount.Field) { f.Choices = []firstaccount.Choice{{Value: "1", Label: "One"}} },
	} {
		candidate := description
		candidate.Fields = append([]firstaccount.Field(nil), description.Fields...)
		mutate(&candidate.Fields[2])
		seal(&candidate)
		if firstaccount.ValidateDescription(candidate) == nil {
			t.Fatal("digest hid invalid collection shape")
		}
	}
	for _, replacement := range []string{`"max_items":null`, `"max_items":3,"max_items":3`, `"max_items":3.0`} {
		bad := bytes.Replace(document, []byte(`"max_items":3`), []byte(replacement), 1)
		if _, err := firstaccountwire.DecodeDescription(bad); err == nil {
			t.Fatal("noncanonical collection limit accepted")
		}
	}
}

func TestResponseBindsOperationAndPreservesCommitUnknownCleanup(t *testing.T) {
	for _, outcome := range []firstaccountwire.Outcome{firstaccountwire.Created, firstaccountwire.Unknown, firstaccountwire.NotCreated} {
		for _, cleanup := range []bool{false, true} {
			response := firstaccountwire.Response{Version: firstaccountprotocol.Version, OperationID: operationID, Outcome: outcome, CleanupFailed: cleanup}
			if outcome == firstaccountwire.Unknown {
				response.Code = firstaccountwire.OutcomeUnknown
			}
			if outcome == firstaccountwire.NotCreated {
				response.Code = firstaccountwire.NotFresh
			}
			document, err := firstaccountwire.EncodeResponse(response)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := firstaccountwire.DecodeResponse(document, operationID)
			if err != nil || parsed != response {
				t.Fatal("outcome/cleanup metadata changed", err)
			}
			if _, err := firstaccountwire.DecodeResponse(document, fingerprint); err == nil {
				t.Fatal("foreign operation response accepted")
			}
		}
	}
	response := firstaccountwire.Response{Version: firstaccountprotocol.Version, OperationID: operationID, Outcome: firstaccountwire.Created}
	document, _ := firstaccountwire.EncodeResponse(response)
	for _, bad := range [][]byte{
		bytes.Replace(document, []byte(`"outcome":"created"`), []byte(`"outcome":"unknown"`), 1),
		bytes.Replace(document, []byte(`"cleanup_failed":false`), []byte(`"cleanup_failed":false,"cleanup_failed":false`), 1),
		bytes.Replace(document, []byte(`,"cleanup_failed":false`), nil, 1),
		bytes.Replace(document, []byte(`"cleanup_failed":false`), []byte(`"cleanup_failed":null`), 1),
		bytes.Replace(document, []byte(`"code":""`), []byte(`"code":"password-private"`), 1),
		bytes.Replace(document, []byte(operationID), []byte(strings.Repeat("0", 64)), 1),
		bytes.Replace(document, []byte(operationID), nil, 1),
		append(bytes.Clone(document), []byte("{}")...),
	} {
		if _, err := firstaccountwire.DecodeResponse(bad, operationID); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
	if _, err := firstaccountwire.DecodeResponse(document, ""); err == nil {
		t.Fatal("response without parent operation accepted")
	}
	for _, code := range []firstaccountwire.Code{firstaccountwire.InvalidRequest, firstaccountwire.InputTransportFailed, firstaccountwire.Canceled} {
		document, err := firstaccountwire.EncodeResponse(firstaccountwire.Response{Version: firstaccountprotocol.Version, Outcome: firstaccountwire.NotCreated, Code: code})
		if err != nil {
			t.Fatal(err)
		}
		if response, err := firstaccountwire.DecodeResponse(document, operationID); err != nil || response.OperationID != "" || response.Code != code {
			t.Fatal("pre-acquisition failure requires nonexistent parsed operation")
		}
	}
}

func FuzzResponse(f *testing.F) {
	document, _ := firstaccountwire.EncodeResponse(firstaccountwire.Response{Version: firstaccountprotocol.Version, OperationID: operationID, Outcome: firstaccountwire.Created})
	f.Add(document)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		response, err := firstaccountwire.DecodeResponse(input, operationID)
		if err != nil {
			return
		}
		encoded, err := firstaccountwire.EncodeResponse(response)
		if err != nil || !bytes.Equal(encoded, input) {
			t.Fatal("accepted response is not canonical")
		}
	})
}

func FuzzDescription(f *testing.F) {
	description := descriptionFixture(f)
	document, _ := firstaccountwire.EncodeDescription(firstaccountwire.DescriptionResponse{Version: firstaccountprotocol.Version, Description: &description})
	f.Add(document)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		response, err := firstaccountwire.DecodeDescription(input)
		if err != nil {
			return
		}
		encoded, err := firstaccountwire.EncodeDescription(response)
		if err != nil || !bytes.Equal(encoded, input) {
			t.Fatal("accepted description is not canonical")
		}
	})
}
