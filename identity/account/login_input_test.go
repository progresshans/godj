package identityaccount

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web/sessionauth"
)

type declaredLoginGuard struct {
	constructionGuard
	input   auth.LoginInput
	failure error
}

func (guard declaredLoginGuard) LoginInput() (auth.LoginInput, error) {
	return guard.input, guard.failure
}

func TestAccountLoginFormUsesDeclaredLengthAndNormalization(t *testing.T) {
	for _, mode := range []string{"exact", "nfkc", "metadata_failure", "missing_metadata"} {
		t.Run(mode, func(t *testing.T) {
			store, err := sessions.NewMemoryStore(1)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := sessions.NewManager(store, sessions.Config{})
			if err != nil {
				t.Fatal(err)
			}
			policy := auth.LoginExact
			if mode == "nfkc" {
				policy = auth.LoginUnicode16NFKC
			}
			input, err := auth.NewLoginInput(auth.LoginInputConfig{Label: "Contact", MaxLength: 5, Normalization: policy})
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("private login metadata failure")
			guard := declaredLoginGuard{constructionGuard: constructionGuard{t, manager}, input: input}
			if mode == "metadata_failure" {
				guard.failure = fault
			}
			if mode == "missing_metadata" {
				guard.input = auth.LoginInput{}
			}
			runtime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: guard, Authorizer: guard})
			if err != nil {
				t.Fatal(err)
			}
			app := &Application{auth: runtime}
			err = app.prepareForms()
			if mode == "metadata_failure" || mode == "missing_metadata" {
				if err == nil || mode == "metadata_failure" && !errors.Is(err, fault) {
					t.Fatal("metadata failure inferred default form", err)
				}
				if strings.Contains(err.Error(), fault.Error()) {
					t.Fatal("metadata cause leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if app.login.Fields()[0].Label() != "Contact" {
				t.Fatal("form label came from another account")
			}
			for _, raw := range []string{"  ｕｓｅｒ  ", "ﬃﬃ", "abcdef"} {
				form, err := bind(t.Context(), app.login, url.Values{"username": {raw}, "password": {"  secret  "}})
				if err != nil {
					t.Fatal(err)
				}
				invalid := raw == "abcdef" || mode == "nfkc" && raw == "ﬃﬃ"
				if form.Valid() == invalid {
					t.Fatal("selected length/normalization ignored", mode, raw)
				}
				if invalid {
					if form.Errors().ByField("username").Empty() {
						t.Fatal("length failure lost field")
					}
					continue
				}
				want := strings.TrimSpace(raw)
				if mode == "nfkc" {
					want = "user"
				}
				if actual, present := form.Cleaned().String("username"); !present || actual != want {
					t.Fatal("login form applied a different normalization")
				}
				if actual, present := form.Cleaned().String("password"); !present || actual != "  secret  " {
					t.Fatal("login metadata changed password cleaning")
				}
			}
		})
	}
}
