package validation_test

import (
	"github.com/progresshans/godj/internal/urltest"
	"github.com/progresshans/godj/validation"
	"strings"
	"testing"
)

func TestURLValidatorAgainstPinnedDjango(t *testing.T) {
	reference, inputs := urltest.Load(t, "sqlite")
	for _, input := range inputs {
		t.Run(input.Name, func(t *testing.T) {
			if input.Value == nil {
				return
			}
			want, found := reference.Validator[input.Name]
			if !found || validation.ValidURL(*input.Value) != want {
				t.Fatalf("URL grammar differs from independent Django observation: want %v", want)
			}
		})
	}
}

func FuzzURLValidator(f *testing.F) {
	_, inputs := urltest.Load(f, "sqlite")
	for _, input := range inputs {
		if input.Value != nil {
			f.Add(*input.Value)
		}
	}
	f.Fuzz(func(t *testing.T, value string) {
		valid := validation.ValidURL(value)
		// Scheme case changes do not change the complete-URL grammar.
		if scheme, tail, found := strings.Cut(value, "://"); found {
			switch strings.ToLower(scheme) {
			case "http", "https", "ftp", "ftps":
				if valid != validation.ValidURL(strings.ToUpper(scheme)+"://"+tail) {
					t.Fatal("scheme case changed URL validity")
				}
			}
		}
	})
}
