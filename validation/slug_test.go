package validation_test

import (
	"testing"

	"github.com/progresshans/godj/internal/slugtest"
	"github.com/progresshans/godj/validation"
)

func TestSlugValidatorAgainstPinnedDjango(t *testing.T) {
	reference, cases := slugtest.Load(t, "sqlite")
	for _, mode := range []string{"ascii", "unicode"} {
		for _, input := range cases {
			t.Run(mode+"/"+input.Name, func(t *testing.T) {
				if input.Value == nil {
					// RegexValidator stringifies Python None; Go's pure validator
					// accepts a string. Form/JSON null is exercised separately.
					if !reference.Validators[mode][input.Name] {
						t.Fatal("native None coercion changed")
					}
					return
				}
				want, exists := reference.Validators[mode][input.Name]
				if !exists || validation.ValidSlug(*input.Value, mode == "unicode") != want {
					t.Fatal("slug grammar differs from native", input.Name)
				}
			})
		}
	}
	for _, value := range []string{"\xff", "valid\xc0\x80", "\xed\xa0\x80"} {
		if validation.ValidSlug(value, false) || validation.ValidSlug(value, true) {
			t.Fatal("invalid UTF-8 accepted")
		}
	}
}

func FuzzSlugValidator(f *testing.F) {
	_, cases := slugtest.Load(f, "sqlite")
	for _, input := range cases {
		if input.Value != nil {
			f.Add(*input.Value)
		}
	}
	f.Fuzz(func(t *testing.T, value string) {
		ascii, unicode := validation.ValidSlug(value, false), validation.ValidSlug(value, true)
		if ascii && !unicode {
			t.Fatal("ASCII acceptance escaped the Unicode superset")
		}
		if validation.ValidSlug(value+"\n", false) || validation.ValidSlug(value+"\n", true) {
			t.Fatal("terminal LF accepted")
		}
		if validation.ValidSlug(value+"/", false) || validation.ValidSlug(value+"/", true) {
			t.Fatal("path separator accepted")
		}
	})
}
