package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

type profileTextBackend struct{ calls int }

func (backend *profileTextBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	backend.calls++
	return errors.New("unexpected profile text snapshot")
}

func TestSelectedProfileRejectsInvalidStoredTextBeforeProjection(t *testing.T) {
	definition, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := Bind(modeldef.AccountRole(), models.UserDescriptor{}, definition)
	if err != nil {
		t.Fatal(err)
	}
	backend := &profileTextBackend{}
	store, err := NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewProfileReader(store, auth.PrincipalAuthorizer{}, ProfileReadConfig{Fields: []string{"username"}, ViewPermission: "account.view"})
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := binding.CredentialDomain([32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	row := models.NewUserWithID(17)
	row.Username, row.PrincipalID, row.EncodedPassword = "profile-user", "profile-principal", "private stored credential"
	row.Active, row.Revision = true, 1
	row.DateJoined = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		change func(*models.User)
	}{
		{"username_length", func(row *models.User) { row.Username = strings.Repeat("x", 257) }},
		{"profile_character_length", func(row *models.User) { row.FirstName = strings.Repeat("한", 151) }},
		{"email_length", func(row *models.User) { row.Email = strings.Repeat("x", 255) }},
		{"profile_utf8", func(row *models.User) { row.FirstName = "private profile\xff" }},
		{"profile_nul", func(row *models.User) { row.FirstName = "private profile\x00" }},
		{"email_utf8", func(row *models.User) { row.Email = "private email\xff" }},
		{"email_nul", func(row *models.User) { row.Email = "private email\x00" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := row
			test.change(&candidate)
			// Get/List use this exact projection even when a damaged profile
			// field is not exposed. History uses the same stored-value check.
			profile, err := reader.project(namespace, candidate, nil)
			if err == nil || profile.ID() != 0 || profile.Revision() != 0 || profile.View().Values() != nil {
				t.Fatal("invalid stored text published a selected profile", test.name)
			}
			values, failure := reader.storedValues(candidate)
			if values != nil || failure == nil || !errors.Is(failure, &Error{Code: CodePersistence}) {
				t.Fatal("stored profile damage published values or lost classification", failure)
			}
			if _, rejected := validation.Rejected(failure); rejected {
				t.Fatal("stored data failure became ordinary input diagnostics")
			}
			encoded, marshalErr := json.Marshal(failure)
			if marshalErr != nil || strings.Contains(string(encoded)+fmt.Sprintf("%v %+v %#v", failure, failure, failure), "private") {
				t.Fatal("stored profile failure leaked its value", marshalErr)
			}
		})
	}
	// Stored values are observed exactly. Neither trimming, email validation nor
	// normalization is appropriate here; historical representable text survives.
	row.Username, row.FirstName, row.Email = "Ｆｒｅｄ", strings.Repeat("한", 150), strings.Repeat("x", 254)
	reader, err = NewProfileReader(store, auth.PrincipalAuthorizer{}, ProfileReadConfig{Fields: []string{"username", "first_name", "email"}, ViewPermission: "account.view"})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{row.Email, "  legacy-address  ", "Mixed@EXAMPLE.COM"} {
		row.Email = email
		profile, err := reader.project(namespace, row, nil)
		if err != nil || profile.ID() != row.ID || profile.Revision() != row.Revision {
			t.Fatal("representable stored text was rejected", err)
		}
		for name, expected := range map[string]string{"username": row.Username, "first_name": row.FirstName, "email": email} {
			actual, present := profile.View().Get(name)
			if !present || !actual.Equal(query.String(expected)) {
				t.Fatal("stored projection applied an input policy", name)
			}
		}
	}
	if backend.calls != 0 {
		t.Fatal("pure profile projection performed I/O")
	}
}
