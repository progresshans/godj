package auth

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPasswordResetAuthorityRequiresSameOwner(t *testing.T) {
	var zero PasswordResetAuthority
	if zero.Valid() || zero.Matches(zero) {
		t.Fatal("zero authority matched")
	}
	owners := make([]PasswordResetAuthority, 128)
	for index := range owners {
		owner := NewPasswordResetAuthority()
		copy := owner
		if !owner.Valid() || !owner.Matches(copy) || owner.Matches(zero) || zero.Matches(owner) {
			t.Fatal("authority identity is not owned")
		}
		for _, previous := range owners[:index] {
			if owner.Matches(previous) {
				t.Fatal("different reset owners matched")
			}
		}
		owners[index] = owner
	}
}

func TestPasswordResetEmailMatchesNativeForm(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		raw, err := os.ReadFile("../internal/identitytest/testdata/password-reset-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Django       string
			Observations struct {
				Inputs []struct {
					Label, Input, Cleaned string
					Valid                 bool
					Errors                map[string][]string
				} `json:"request_input"`
			}
		}
		if err := json.Unmarshal(raw, &fixture); err != nil || fixture.Django != "6.1" || len(fixture.Observations.Inputs) != 9 {
			t.Fatal("incomplete native reset input reference", err)
		}
		for _, row := range fixture.Observations.Inputs {
			t.Run(backend+"/"+row.Label, func(t *testing.T) {
				cleaned, failures := CleanPasswordResetEmail(row.Input)
				actual := map[string][]string{}
				for _, failure := range failures.All() {
					actual[string(failure.Field())] = append(actual[string(failure.Field())], string(failure.Code()))
				}
				if cleaned != row.Cleaned || failures.Empty() != row.Valid || !reflect.DeepEqual(actual, row.Errors) {
					t.Fatal("native reset input behavior differs", actual, row.Errors)
				}
			})
		}
	}
	for _, raw := range []string{"bad\xff@example.test", strings.Repeat(" ", 4097)} {
		if cleaned, failures := CleanPasswordResetEmail(raw); cleaned != "" || failures.Empty() {
			t.Fatal("Go input guard was bypassed")
		}
	}
}
