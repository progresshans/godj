// Package identityreference embeds the single captured native identity oracle
// for both selected-model and legacy test consumers. It imports no DB runtime.
package identityreference

import (
	"embed"
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
		Django, Backend string
		Observations    struct {
			Cases []ResetRequest `json:"request_selection"`
		}
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		return nil, err
	}
	if reference.Django != "6.1" || len(reference.Observations.Cases) != 12 || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[backend] {
		return nil, errors.New("incomplete or mismatched password reset native reference")
	}
	return reference.Observations.Cases, nil
}
