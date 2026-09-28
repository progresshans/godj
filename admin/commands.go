package admin

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
)

// CommandConfig declares an object-specific operation with its own inputs.
// Commands do not become editable model fields. Run owns the transaction and
// must check the submitted revision there, just like an Update callback.
type CommandConfig struct {
	Name       string
	Label      string
	Permission auth.Permission
	Form       forms.Spec
	Run        func(context.Context, auth.Principal, Mutation, forms.Values) (CommandResult, error)
}

// CommandResult contains only the confirmed object's identity and revision.
// A command need not reread a profile after committing (for example, after it
// revokes its own actor's sessions). No result is published on error.
type CommandResult struct {
	ID       int64
	Revision int64
}

type CommandDescriptor struct {
	Name       string
	Label      string
	Permission auth.Permission
	FormFields []forms.Field
}

type registeredCommand struct {
	name, label string
	permission  auth.Permission
	form        forms.Spec
	run         func(context.Context, auth.Principal, Mutation, forms.Form) (CommandResult, error)
}

func prepareCommands(configs []CommandConfig, model registeredModel) ([]registeredCommand, error) {
	if len(configs) > MaximumActions {
		return nil, &ConfigError{Path: "model.commands", Code: "limit_exceeded"}
	}
	seen := make(map[string]bool, len(configs))
	result := make([]registeredCommand, 0, len(configs))
	for index, config := range configs {
		path := fmt.Sprintf("model.commands[%d]", index)
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
		if config.Run == nil {
			return nil, &ConfigError{Path: path + ".run", Code: "missing"}
		}
		if _, err := config.Form.Unbound(nil); err != nil {
			return nil, &ConfigError{Path: path + ".form", Code: "invalid", Cause: err}
		}
		fields := config.Form.Fields()
		conditions := 1
		if model.revisionField != "" {
			conditions++
		}
		if len(fields)+conditions > MaximumInputValues {
			return nil, &ConfigError{Path: path + ".form", Code: "limit_exceeded"}
		}
		for _, field := range fields {
			if field.Name() == "csrfmiddlewaretoken" || field.Name() == "expected_revision" || field.ModelChoice() {
				return nil, &ConfigError{Path: path + ".form", Code: "unsupported_field"}
			}
		}
		command := registeredCommand{name: config.Name, label: config.Label, permission: config.Permission, form: config.Form}
		command.run = func(ctx context.Context, principal auth.Principal, mutation Mutation, submitted forms.Form) (CommandResult, error) {
			if err := validatePrincipalPermission(ctx, principal, config.Permission); err != nil {
				return CommandResult{}, err
			}
			if err := model.validateMutation(mutation); err != nil {
				return CommandResult{}, err
			}
			data, err := validatedSubmission(submitted, fields)
			if err != nil {
				return CommandResult{}, err
			}
			values, err := validateBoundData(data, config.Form, fields)
			if err != nil {
				return CommandResult{}, err
			}
			outcome, err := config.Run(ctx, principal, mutation, values)
			if err != nil {
				return CommandResult{}, err
			}
			if outcome.ID != mutation.ID || outcome.Revision < mutation.Revision || model.revisionField == "" && outcome.Revision != 0 {
				return CommandResult{}, reconciliationError("command "+config.Name, &ConfigError{Path: "command.result", Code: "mismatch"})
			}
			return outcome, nil
		}
		result = append(result, command)
	}
	return result, nil
}
