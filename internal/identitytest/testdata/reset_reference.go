// Package identityreference embeds the single captured native identity oracle
// for both selected-model and legacy test consumers. It imports no DB runtime.
package identityreference

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
)

//go:embed password-reset-django61-*.json
var resetReferences embed.FS

func PasswordReset(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown password reset reference backend")
	}
	return resetReferences.ReadFile("password-reset-django61-" + backend + ".json")
}

type ResetRequest struct {
	Label, Input, Cleaned string
	Valid                 bool
	Accounts              []struct {
		Username, Email string
		Active, Usable  bool
	}
	Selected, Delivered []string
	UsersUnchanged      bool `json:"users_unchanged"`
}

func ResetRequests(backend string) ([]ResetRequest, error) {
	raw, err := PasswordReset(backend)
	if err != nil {
		return nil, err
	}
	var reference struct {
		Django, Python, Backend string
		Sources                 map[string]string `json:"source_sha256"`
		Observations            struct {
			Cases []ResetRequest `json:"request_selection"`
		}
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		return nil, err
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || len(reference.Sources) != 8 || len(reference.Observations.Cases) != 12 || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[backend] {
		return nil, errors.New("incomplete or mismatched password reset native reference")
	}
	for _, name := range []string{"auth", "base_user", "forms", "hashers", "locmem", "mail_message", "models", "tokens"} {
		value := reference.Sources[name]
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
			return nil, errors.New("missing independent reset source digest")
		}
	}
	expected := map[string]bool{
		"ascii_duplicates": true, "unknown": true, "inactive": true, "unusable": true,
		"quoted": true, "unicode_exact": true, "unicode_case": true, "nfkc_prefilter": true,
		"casefold_prefilter": true, "kelvin_prefilter": true, "turkish_prefilter": true, "unicode_local_exact": true,
	}
	for _, observation := range reference.Observations.Cases {
		if !expected[observation.Label] {
			return nil, errors.New("duplicate or unknown reset recipient observation")
		}
		delete(expected, observation.Label)
	}
	if len(expected) != 0 {
		return nil, errors.New("missing independent reset recipient observation")
	}
	return reference.Observations.Cases, nil
}
