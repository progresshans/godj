package parameters_test

import (
	"net/http"
	"testing"

	"github.com/progresshans/godj/api/parameters"
)

func TestStringChoicesOwnDomainDefaultAndPresence(t *testing.T) {
	choices := []string{"all", "same", "different", "é"}
	codec := parameters.StringChoices(choices...)
	choices[0] = "changed"
	type request struct{ Choice string }
	query, err := parameters.New(128, parameters.Field("comparison", parameters.Default(codec, "all"), func(out *request, value string) { out.Choice = value }, ""))
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]string{"": "all", "comparison=all": "all", "comparison=same": "same", "comparison=different": "different", "comparison=%C3%A9": "é"} {
		value, diagnostics, err := query.Parse(raw)
		if err != nil || !diagnostics.Empty() || value.Choice != want {
			t.Fatal("choice/default differs", raw, value, diagnostics, err)
		}
	}
	for _, raw := range []string{"comparison=", "comparison=changed", "comparison=Same", "comparison=+same", "comparison=same+", "comparison=same&comparison=same", "comparison=%00", "comparison=%ff"} {
		value, diagnostics, err := query.Parse(raw)
		if err != nil || diagnostics.Empty() || value != (request{}) {
			t.Fatal("invalid choice assigned a DTO", raw, value, err)
		}
	}
	for _, choices := range [][]string{nil, {"same", "same"}, {"\x00"}, {"\xff"}} {
		if _, err := parameters.New(128, parameters.Field("comparison", parameters.Required(parameters.StringChoices(choices...)), func(out *request, value string) { out.Choice = value }, "")); err == nil {
			t.Fatal("invalid choices accepted", choices)
		}
	}
	if _, err := parameters.New(128, parameters.Field("comparison", parameters.Default(codec, "unknown"), func(out *request, value string) { out.Choice = value }, "")); err == nil {
		t.Fatal("out-of-domain default accepted")
	}
	optional, err := parameters.New(128, parameters.Field("choice", parameters.Optional(parameters.StringChoices("", "same")), func(out **string, value *string) { *out = value }, ""))
	if err != nil {
		t.Fatal(err)
	}
	absent, diagnostics, err := optional.Parse("")
	if err != nil || !diagnostics.Empty() || absent != nil {
		t.Fatal("omission became present", err)
	}
	present, diagnostics, err := optional.Parse("choice=")
	if err != nil || !diagnostics.Empty() || present == nil || *present != "" {
		t.Fatal("declared empty choice was lost", err)
	}
	header, err := parameters.NewHeader("X-Choice", parameters.Default(codec, "all"), 9, "")
	if err != nil {
		t.Fatal(err)
	}
	value, diagnostics, err := header.Parse(http.Header{"X-Choice": {"different"}})
	if err != nil || !diagnostics.Empty() || value != "different" {
		t.Fatal("header choices diverged", err)
	}
	if _, err := parameters.NewHeader("X-Choice", parameters.Default(codec, "all"), 8, ""); err == nil {
		t.Fatal("header cannot represent every declared choice")
	}
}
