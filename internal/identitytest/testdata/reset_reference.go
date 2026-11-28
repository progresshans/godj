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

type ResetObservation struct {
	Requests               []ResetRequest `json:"request_selection"`
	Bindings               map[string]map[string]bool
	Clock, Keys, Confirmed map[string]bool
}

func ResetObservations(backend string) (ResetObservation, error) {
	raw, err := PasswordReset(backend)
	if err != nil {
		return ResetObservation{}, err
	}
	var reference struct {
		Django, Python, Backend string
		Sources                 map[string]string `json:"source_sha256"`
		Observations            ResetObservation
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		return ResetObservation{}, err
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || len(reference.Sources) != 8 || len(reference.Observations.Requests) != 12 || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[backend] {
		return ResetObservation{}, errors.New("incomplete or mismatched password reset native reference")
	}
	for _, name := range []string{"auth", "base_user", "forms", "hashers", "locmem", "mail_message", "models", "tokens"} {
		value := reference.Sources[name]
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
			return ResetObservation{}, errors.New("missing independent reset source digest")
		}
	}
	expected := map[string]bool{
		"ascii_duplicates": true, "unknown": true, "inactive": true, "unusable": true,
		"quoted": true, "unicode_exact": true, "unicode_case": true, "nfkc_prefilter": true,
		"casefold_prefilter": true, "kelvin_prefilter": true, "turkish_prefilter": true, "unicode_local_exact": true,
	}
	for _, observation := range reference.Observations.Requests {
		if !expected[observation.Label] {
			return ResetObservation{}, errors.New("duplicate or unknown reset recipient observation")
		}
		delete(expected, observation.Label)
	}
	if len(expected) != 0 {
		return ResetObservation{}, errors.New("missing independent reset recipient observation")
	}
	bindings := []string{"unchanged", "password", "same_password", "last_login_second", "last_login_microsecond", "email", "email_case", "username", "profile", "inactive", "unusable"}
	if len(reference.Observations.Bindings) != len(bindings) {
		return ResetObservation{}, errors.New("incomplete reset state binding observations")
	}
	groups := []struct {
		values map[string]bool
		names  []string
	}{
		{reference.Observations.Clock, []string{"same", "within", "boundary", "expired", "future"}},
		{reference.Observations.Keys, []string{"same_state_same_second", "old_with_fallback", "old_without_fallback"}},
		{reference.Observations.Confirmed, []string{"last_login_unchanged", "new_password_valid", "old_token_valid", "trimmed_password_valid"}},
	}
	for _, name := range bindings {
		groups = append(groups, struct {
			values map[string]bool
			names  []string
		}{reference.Observations.Bindings[name], []string{"active", "password_changed", "usable", "valid"}})
	}
	for _, group := range groups {
		if len(group.values) != len(group.names) {
			return ResetObservation{}, errors.New("incomplete reset boolean observation")
		}
		for _, name := range group.names {
			if _, found := group.values[name]; !found {
				return ResetObservation{}, errors.New("missing reset boolean observation")
			}
		}
	}
	return reference.Observations, nil
}
