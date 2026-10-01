package admin

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
)

// CollectionCommandConfig resolves or creates an object from its own inputs,
// without requiring an existing selected object. Run owns the transaction,
// current scope checks and any audit writes. It must return only after commit.
// Ordinary object commands retain their separate identity/revision contract.
type CollectionCommandConfig struct {
	Name                  string
	Label                 string
	Permission            auth.Permission
	AdditionalPermissions []auth.Permission
	Form                  forms.Spec
	Run                   func(context.Context, auth.Principal, forms.Values) (CollectionCommandResult, error)
}

// CollectionCommandResult identifies the confirmed object. Created is false
// when the command reused an existing object. No result is published on error.
type CollectionCommandResult struct {
	ID      int64
	Created bool
}

type CollectionCommandDescriptor struct {
	Name        string
	Label       string
	Permissions []auth.Permission
	FormFields  []forms.Field
}

type registeredCollectionCommand struct {
	name, label string
	permissions []auth.Permission
	form        forms.Spec
	run         func(context.Context, auth.Principal, forms.Form) (CollectionCommandResult, error)
}

func prepareCollectionCommands(configs []CollectionCommandConfig, model registeredModel) ([]registeredCollectionCommand, error) {
	if len(configs) > MaximumActions {
		return nil, &ConfigError{Path: "model.collection_commands", Code: "limit_exceeded"}
	}
	seen := make(map[string]bool, len(configs))
	result := make([]registeredCollectionCommand, 0, len(configs))
	for index, config := range configs {
		path := fmt.Sprintf("model.collection_commands[%d]", index)
		if !validSlug(config.Name) || seen[config.Name] {
			return nil, &ConfigError{Path: path + ".name", Code: "invalid_or_duplicate"}
		}
		seen[config.Name] = true
		if config.Label == "" || len(config.Label) > MaximumDisplayBytes || !utf8.ValidString(config.Label) || containsUnsafeControl(config.Label) {
			return nil, &ConfigError{Path: path + ".label", Code: "invalid"}
		}
		if err := validatePermission(path+".permission", config.Permission); err != nil {
			return nil, err
		}
		permissions, err := additionalPermissions(config.Permission, config.AdditionalPermissions)
		if err != nil {
			return nil, err
		}
		if config.Run == nil {
			return nil, &ConfigError{Path: path + ".run", Code: "missing"}
		}
		if _, err := config.Form.Unbound(nil); err != nil {
			return nil, &ConfigError{Path: path + ".form", Code: "invalid", Cause: err}
		}
		fields := config.Form.Fields()
		if len(fields)+1 > MaximumInputValues {
			return nil, &ConfigError{Path: path + ".form", Code: "limit_exceeded"}
		}
		for _, field := range fields {
			if field.Name() == "csrfmiddlewaretoken" || field.Name() == "expected_revision" || field.ModelChoice() {
				return nil, &ConfigError{Path: path + ".form", Code: "unsupported_field"}
			}
		}
		command := registeredCollectionCommand{name: config.Name, label: config.Label, permissions: permissions, form: config.Form}
		command.run = func(ctx context.Context, principal auth.Principal, submitted forms.Form) (CollectionCommandResult, error) {
			if err := validatePrincipalRead(ctx, principal, model.permissions); err != nil {
				return CollectionCommandResult{}, err
			}
			for _, permission := range permissions {
				if err := validatePrincipalPermission(ctx, principal, permission); err != nil {
					return CollectionCommandResult{}, err
				}
			}
			data, err := validatedSubmission(submitted, fields)
			if err != nil {
				return CollectionCommandResult{}, err
			}
			values, err := validateBoundData(ctx, data, config.Form, fields)
			if err != nil {
				return CollectionCommandResult{}, err
			}
			outcome, err := config.Run(ctx, principal, values)
			if err != nil {
				return CollectionCommandResult{}, err
			}
			if outcome.ID <= 0 {
				return CollectionCommandResult{}, reconciliationError("collection command "+config.Name, &ConfigError{Path: "collection_command.result", Code: "invalid_identity"})
			}
			return outcome, nil
		}
		result = append(result, command)
	}
	return result, nil
}
