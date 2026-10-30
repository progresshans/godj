package passwordpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestDefaultDictionaryRequiresExactPinnedBytes(t *testing.T) {
	for _, document := range [][]byte{nil, []byte("truncated"), append(bytes.Clone(commonPasswordData), 0)} {
		if words, err := loadDefaultCommonPasswords(document); err != ErrInvalidConfig || words != nil {
			t.Fatal("unverified default dictionary published")
		}
	}
	words, err := loadDefaultCommonPasswords(commonPasswordData)
	if err != nil || len(words) != 19640 {
		t.Fatal("pinned dictionary is unavailable", err)
	}
}

func TestStrengthRulesUseIndependentAttributesWithoutRetainingValues(t *testing.T) {
	validator, err := NewSimilarityValidator(0)
	if err != nil {
		t.Fatal(err)
	}
	attributes := []Attribute{{Label: "display name", Value: "profile-marker"}}
	diagnostics := validator.ValidatePassword("profile-marker", attributes)
	if len(diagnostics.All()) != 1 || diagnostics.All()[0].Code() != "password_too_similar" {
		t.Fatal("custom profile was ignored")
	}
	params := diagnostics.All()[0].Params()
	if len(params) != 1 || params[0].Key() != "verbose_name" || params[0].Value() != "display name" {
		t.Fatal("static label or diagnostic changed")
	}
	attributes[0].Value = "unrelated"
	if !validator.ValidatePassword("profile-marker", attributes).Empty() {
		t.Fatal("policy retained an earlier input")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, attributes), "unrelated") {
			t.Fatal("attribute appeared in diagnostics")
		}
	}
	if encoded, err := json.Marshal(attributes); err != nil || bytes.Contains(encoded, []byte("unrelated")) {
		t.Fatal("attribute appeared in JSON")
	}
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			for range 20 {
				if validator.ValidatePassword("profile-marker", []Attribute{{Label: "custom", Value: "profile-marker"}}).Empty() ||
					!validator.ValidatePassword("profile-marker", attributes).Empty() {
					t.Error("concurrent profile policy shared input")
				}
			}
		})
	}
	workers.Wait()
}
