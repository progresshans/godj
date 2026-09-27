package identityadmin

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/progresshans/godj/forms"
)

//go:embed testdata/inputs.json
var referenceInputs []byte

//go:embed testdata/django61.json
var djangoReference []byte

type namedInput struct{ Name, Value string }
type formObservation struct {
	Name     string
	Valid    bool
	Username *string
	Codes    map[string][]string
}
type inputCorpus struct {
	Usernames, Emails []namedInput
	Passwords         []struct{ Name, Password1, Password2 string }
}
type formReference struct {
	Django, Python, Unicode string
	InputSHA256             string `json:"input_sha256"`
	Usernames, Passwords    []formObservation
	Emails                  []struct {
		Name  string
		Valid bool
	}
}

func TestIdentityFormDjangoReferenceSubset(t *testing.T) {
	var inputs inputCorpus
	var expected formReference
	if err := json.Unmarshal(referenceInputs, &inputs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(djangoReference, &expected); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(referenceInputs)
	if expected.InputSHA256 != hex.EncodeToString(hash[:]) || expected.Django != "6.1" || expected.Python != "3.14.3" || expected.Unicode != "16.0.0" {
		t.Fatal("reference source/input binding mismatch")
	}
	if len(inputs.Usernames) != len(expected.Usernames) || len(inputs.Passwords) != len(expected.Passwords) || len(inputs.Emails) != len(expected.Emails) {
		t.Fatal("reference omitted inputs")
	}
	registry := registeredIdentityForTest(t)
	descriptor, _ := registry.Lookup("godj_identity", "user")
	spec, err := forms.NewSpec(descriptor.CreateFormFields, creationPasswords())
	if err != nil {
		t.Fatal(err)
	}
	observe := func(data map[string][]string) formObservation {
		form, err := spec.Bind(forms.NewData(data), nil)
		if err != nil {
			t.Fatal(err)
		}
		result := formObservation{Valid: form.Valid(), Codes: map[string][]string{}}
		if username, present := form.Cleaned().String("username"); present {
			result.Username = &username
		}
		for _, failure := range form.Errors().All() {
			result.Codes[string(failure.Field())] = append(result.Codes[string(failure.Field())], string(failure.Code()))
		}
		return result
	}
	for i, input := range inputs.Usernames {
		t.Run("username/"+input.Name, func(t *testing.T) {
			want := expected.Usernames[i]
			got := observe(map[string][]string{"username": {input.Value}, "password1": {"reference-secret"}, "password2": {"reference-secret"}})
			got.Name = input.Name
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("username behavior differs: got %+v, reference %+v", got, want)
			}
		})
	}
	for i, input := range inputs.Passwords {
		t.Run("confirmation/"+input.Name, func(t *testing.T) {
			want := expected.Passwords[i]
			got := observe(map[string][]string{"username": {"Reference"}, "password1": {input.Password1}, "password2": {input.Password2}})
			got.Name = input.Name
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("confirmation behavior differs: got %+v, reference %+v", got, want)
			}
		})
	}
	for i, input := range inputs.Emails {
		t.Run("email/"+input.Name, func(t *testing.T) {
			if want := expected.Emails[i]; want.Name != input.Name || validEmail(input.Value) != want.Valid {
				t.Fatal("EmailValidator behavior differs", input.Name, validEmail(input.Value), want.Valid)
			}
		})
	}
}
