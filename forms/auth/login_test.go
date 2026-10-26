package authform_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	authform "github.com/progresshans/godj/forms/auth"
	"github.com/progresshans/godj/validation"
)

func TestLoginSpecUsesExplicitSourceAndBoundsBeforeAndAfterNormalization(t *testing.T) {
	for _, policy := range []auth.LoginNormalization{auth.LoginExact, auth.LoginUnicode16NFKC} {
		t.Run(string(policy), func(t *testing.T) {
			input, err := auth.NewLoginInput(auth.LoginInputConfig{Label: "Contact", MaxLength: 5, Normalization: policy})
			if err != nil {
				t.Fatal(err)
			}
			spec, err := authform.NewLoginSpec(input, 12)
			if err != nil {
				t.Fatal(err)
			}
			fields := spec.Fields()
			if len(fields) != 2 || fields[0].Name() != "username" || fields[0].Label() != "Contact" || fields[0].MaxLength() != 5 || fields[1].Widget() != forms.PasswordInput {
				t.Fatal("login declaration or private password widget lost")
			}
			for _, test := range []struct {
				name, raw, want string
				valid           bool
			}{
				{"whitespace", "\u0085\u001cuser\u3000", "user", true},
				{"fullwidth", "  ｕｓｅｒ  ", "ｕｓｅｒ", true},
				{"astral_runes", "😀😀😀😀😀", "😀😀😀😀😀", true},
				{"expansion", "ﬃﬃ", "ﬃﬃ", policy == auth.LoginExact},
				// Six input characters would normalize down to three; they must
				// still fail the declared five-character input bound.
				{"contraction", "A\u030aA\u030aA\u030a", "", false},
				{"overlong", "abcdef", "", false},
				{"empty", "\u001c\u3000", "", false},
				{"nul", "a\x00b", "", false},
				{"utf8", "\xff", "", false},
			} {
				t.Run(test.name, func(t *testing.T) {
					bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"username": {test.raw}, "password": {"  secret  "}}), nil)
					if err != nil {
						t.Fatal(err)
					}
					if bound.Valid() != test.valid {
						t.Fatal("selected input policy not enforced")
					}
					if !test.valid {
						if bound.Errors().ByField("username").Empty() {
							t.Fatal("invalid login lost its field")
						}
						return
					}
					want := test.want
					if test.name == "fullwidth" && policy == auth.LoginUnicode16NFKC {
						want = "user"
					}
					if got, ok := bound.Cleaned().String("username"); !ok || got != want {
						t.Fatal("unexpected username cleaning")
					}
					if got, ok := bound.Cleaned().String("password"); !ok || got != "  secret  " {
						t.Fatal("password was transformed")
					}
				})
			}
		})
	}
}

func TestLoginSpecRejectsByteOverflowAndMissingMetadata(t *testing.T) {
	input, err := auth.NewLoginInput(auth.LoginInputConfig{Label: "Login", MaxLength: auth.MaximumUsernameBytes, Normalization: auth.LoginUnicode16NFKC})
	if err != nil {
		t.Fatal(err)
	}
	for _, maximum := range []int{0, -1, 1<<20 + 1} {
		if _, err := authform.NewLoginSpec(input, maximum); err == nil {
			t.Fatal("unbounded password accepted")
		}
	}
	if _, err := authform.NewLoginSpec(auth.LoginInput{}, 12); err == nil {
		t.Fatal("implicit account metadata")
	}
	spec, err := authform.NewLoginSpec(input, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, username, password, field string
		valid                           bool
	}{
		{"username_byte_limit", strings.Repeat("界", 342), "secret", "username", false},
		{"username_pre_nfkc_bytes", strings.Repeat("Ａ", 342), "secret", "username", false},
		{"username_post_nfkc_bytes", strings.Repeat("㍿", 100), "secret", "username", false},
		{"password_byte_limit", "user", strings.Repeat("界", 5), "password", false},
		{"password_boundary", "user", strings.Repeat("界", 4), "", true},
		{"password_spaces", "user", "    ", "", true},
		{"missing_password", "user", "", "password", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"username": {test.username}, "password": {test.password}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			if bound.Valid() != test.valid || !test.valid && bound.Errors().ByField(validation.Field(test.field)).Empty() {
				t.Fatal("byte/required boundary not enforced")
			}
			if test.valid {
				if got, ok := bound.Cleaned().String("password"); !ok || got != test.password {
					t.Fatal("password bytes changed")
				}
			}
		})
	}
}
