// Package firstaccount connects a project's typed initial account declaration
// to an operator command. Description and input cleaning do not acquire a DB.
// The process owner controls acquisition, closing and durable result publication.
package firstaccount

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

const (
	MaxFields           = 31
	MaxFieldBytes       = 4096
	MaxInputBytes       = 65536
	MaxChoices          = 256
	MaxDescriptionBytes = 65536
)

// Backend is one real resource acquired for an explicit operator invocation.
// It is independent of the project's migration opener and must be closed once
// by its process owner, including a resource returned alongside an open error.
// Open and the typed owner validate its native relation-write capability.
type Backend interface {
	db.SnapshotReader
	Close() error
}

// Field describes a selected scalar using its actual model/form meaning.
// Submitted values and application callbacks are never part of a description.
type Field struct {
	Name       string   `json:"name"`
	Label      string   `json:"label"`
	Kind       string   `json:"kind"`
	Required   bool     `json:"required"`
	Nullable   bool     `json:"nullable"`
	HasDefault bool     `json:"has_default"`
	MaxBytes   int      `json:"max_bytes"`
	Choices    []Choice `json:"choices"`
}

// Choice is a declared, static model choice. Its value is the exact ModelForm
// input spelling; terminal renderers must quote it instead of interpreting it.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Description is a detached declaration without submitted values. Its digest binds
// the full host relation source, selected role, declared policy evidence and
// prompt/input shape. It does not attest to arbitrary Go callback behavior.
type Description struct {
	Fingerprint      string  `json:"fingerprint"`
	SourceSHA256     string  `json:"source_sha256"`
	RoleSHA256       string  `json:"role_sha256"`
	PolicySHA256     string  `json:"policy_sha256"`
	EvidenceSHA256   string  `json:"evidence_sha256"`
	Model            string  `json:"model"`
	Login            string  `json:"login"`
	MaxPasswordBytes int     `json:"max_password_bytes"`
	Fields           []Field `json:"fields"`
}

// Value preserves omission separately from an explicitly submitted empty
// scalar. The caller owns Text and should clear it after transport/cleaning.
type Value struct {
	Name    string
	Present bool
	Text    []byte
}

func (Value) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("firstaccount.Value{redacted}"))
}
func (Value) MarshalJSON() ([]byte, error) { return []byte(`"firstaccount.Value{redacted}"`), nil }

type provision func(context.Context, Backend, string, string) (accountmodel.FirstAccountCreation, error)
type Command struct{ state *commandState }
type commandState struct {
	description Description
	open        func(context.Context) (Backend, error)
	clean       func(context.Context, forms.Data) (provision, error)
}

func (Command) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("firstaccount.Command{redacted}"))
}

// New validates the complete declaration without opening a backend or hashing.
// The initial command currently accepts the selected scalar fields; a relation
// or file declaration must have its own input/choice lifetime implemented and
// fails explicitly here instead of being omitted from required profile input.
func New[M any](definition accountmodel.FirstAccountDefinition[M], open func(context.Context) (Backend, error)) (Command, error) {
	if open == nil {
		return Command{}, failure(InvalidConfig, nil)
	}
	binding, err := definition.Binding()
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	role, err := binding.Role()
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	objects := definition.Manager()
	metadata, err := objects.Metadata()
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	selected := definition.Fields()
	if len(selected) == 0 || len(selected) > MaxFields {
		return Command{}, failure(InvalidConfig, nil)
	}
	byName := make(map[string]ir.Field, len(metadata.Fields))
	for _, field := range metadata.Fields {
		byName[field.Name] = field
	}
	for _, name := range selected {
		field, present := byName[name]
		if !present || field.Kind.IsFile() || field.Relation != nil {
			return Command{}, failure(InvalidConfig, &query.Error{Code: query.CodeUnsupported})
		}
	}
	formDefinition, err := binding.FormDefinition(selected)
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	form, err := formDefinition.Spec(metadata)
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	project, err := binding.Project()
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	source, err := project.SourceSHA256()
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	roleSHA, err := binding.RoleFingerprint()
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	description := Description{SourceSHA256: source, RoleSHA256: roleSHA, PolicySHA256: definition.Fingerprint(), EvidenceSHA256: definition.EvidenceSHA256(), Model: role.Model.AppLabel + "." + role.Model.ModelName, Login: role.Login, MaxPasswordBytes: definition.MaxPasswordBytes()}
	for _, field := range form.Fields() {
		if !safeName(field.Name()) || !safeLabel(field.Label()) {
			return Command{}, failure(InvalidConfig, nil)
		}
		_, hasDefault := field.Default()
		choices := make([]Choice, 0, len(field.Choices()))
		for _, choice := range field.Choices() {
			choices = append(choices, Choice{Value: choice.InputValue(), Label: choice.Label})
		}
		maximum := MaxFieldBytes
		if field.MaxInputBytes() > 0 {
			maximum = min(maximum, field.MaxInputBytes())
		}
		description.Fields = append(description.Fields, Field{Name: field.Name(), Label: field.Label(), Kind: string(byName[field.Name()].Kind), Required: field.Required(), Nullable: field.Nullable(), HasDefault: hasDefault, MaxBytes: maximum, Choices: choices})
	}
	// The login prompt precedes profile fields; the remaining order is the IR
	// declaration order, independent of a caller's selection-slice order.
	index := slices.IndexFunc(description.Fields, func(field Field) bool { return field.Name == role.Login })
	if index < 0 {
		return Command{}, failure(InvalidConfig, nil)
	}
	login := description.Fields[index]
	description.Fields = append([]Field{login}, slices.Delete(description.Fields, index, index+1)...)
	description.Fingerprint, err = descriptionFingerprint(description)
	if err != nil {
		return Command{}, failure(InvalidConfig, err)
	}
	if err := ValidateDescription(description); err != nil {
		return Command{}, err
	}
	state := &commandState{description: description, open: open}
	state.clean = func(ctx context.Context, data forms.Data) (provision, error) {
		bound, err := formmodel.BindInstance(ctx, objects, form, data, nil, formmodel.PostClean{})
		if err != nil {
			return nil, failure(InvalidInput, err)
		}
		if !bound.BoundForm().Form().Valid() {
			return nil, validation.Reject(bound.BoundForm().Form().Errors(), nil)
		}
		input, err := formmodel.PrepareInput(objects, bound.BoundForm())
		if err != nil {
			return nil, failure(InvalidInput, err)
		}
		return func(ctx context.Context, backend Backend, operationID, password string) (accountmodel.FirstAccountCreation, error) {
			owner, err := definition.Bind(backend)
			if err != nil {
				return accountmodel.FirstAccountCreation{}, err
			}
			pending, err := owner.PrepareOperation(ctx, operationID, input, password)
			if err != nil {
				return accountmodel.FirstAccountCreation{}, err
			}
			return owner.Commit(ctx, pending)
		}, nil
	}
	return Command{state}, nil
}

func (command Command) Description() (Description, error) {
	if command.state == nil {
		return Description{}, failure(InvalidConfig, nil)
	}
	description := command.state.description
	description.Fields = slices.Clone(description.Fields)
	for i := range description.Fields {
		description.Fields[i].Choices = slices.Clone(description.Fields[i].Choices)
	}
	return description, nil
}

// ValidateDescription is the bounded public-data check used after a process
// handoff. A matching digest is not write authority: Prepare also compares it
// to the actual command's declaration before typed cleaning.
func ValidateDescription(description Description) error {
	if !validDigest(description.Fingerprint) || !validDigest(description.SourceSHA256) || !validDigest(description.RoleSHA256) || !validDigest(description.PolicySHA256) || !validDigest(description.EvidenceSHA256) || description.MaxPasswordBytes < 1 || description.MaxPasswordBytes > 4096 || len(description.Fields) == 0 || len(description.Fields) > MaxFields {
		return failure(InvalidConfig, nil)
	}
	model := strings.Split(description.Model, ".")
	if len(model) != 2 || !safeName(model[0]) || !safeName(model[1]) || !safeName(description.Login) || description.Fields[0].Name != description.Login {
		return failure(InvalidConfig, nil)
	}
	names := make(map[string]bool, len(description.Fields))
	for _, field := range description.Fields {
		if names[field.Name] || !safeName(field.Name) || !safeLabel(field.Label) || field.MaxBytes < 1 || field.MaxBytes > MaxFieldBytes {
			return failure(InvalidConfig, nil)
		}
		names[field.Name] = true
		if len(field.Choices) > MaxChoices {
			return failure(InvalidConfig, nil)
		}
		choices := make(map[string]bool, len(field.Choices))
		for _, choice := range field.Choices {
			if choices[choice.Value] || len(choice.Value) > field.MaxBytes || !utf8.ValidString(choice.Value) || strings.ContainsRune(choice.Value, 0) || choice.Label != "" && !safeLabel(choice.Label) {
				return failure(InvalidConfig, nil)
			}
			choices[choice.Value] = true
		}
		switch ir.FieldKind(field.Kind) {
		case ir.FieldInteger, ir.FieldFloat, ir.FieldDecimal, ir.FieldUUID, ir.FieldBinary, ir.FieldJSON, ir.FieldChar, ir.FieldEmail, ir.FieldURL, ir.FieldSlug, ir.FieldText, ir.FieldDuration, ir.FieldTime, ir.FieldDate, ir.FieldDateTime, ir.FieldBoolean:
		default:
			return failure(InvalidConfig, nil)
		}
	}
	digest, err := descriptionFingerprint(description)
	if err != nil || digest != description.Fingerprint {
		return failure(InvalidConfig, err)
	}
	return nil
}
func descriptionFingerprint(description Description) (string, error) {
	description.Fingerprint = ""
	encoded, err := json.Marshal(description)
	if err != nil {
		return "", err
	}
	if len(encoded)+64 > MaxDescriptionBytes {
		return "", failure(InvalidConfig, nil)
	}
	digest := sha256.Sum256(append([]byte("godj.first-account-command.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:]), nil
}
func (command Command) Open(ctx context.Context) (Backend, error) {
	if err := command.valid(ctx); err != nil {
		return nil, err
	}
	backend, err := command.state.open(ctx)
	if err != nil {
		return backend, failure(OpenFailed, err)
	}
	if nilValue(backend) {
		return backend, failure(InvalidConfig, nil)
	}
	if writer, ok := backend.(db.CoordinatedRelationAtomic); !ok || nilValue(writer) {
		return backend, failure(InvalidConfig, nil)
	}
	return backend, nil
}

type Prepared struct{ state *preparedState }
type preparedState struct {
	owner     *commandState
	provision provision
	attempted atomic.Bool
}

func (Prepared) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("firstaccount.Prepared{redacted}"))
}
func (Prepared) MarshalJSON() ([]byte, error) {
	return []byte(`"firstaccount.Prepared{redacted}"`), nil
}

// Clear abandons an unclaimed preparation. It never alters an in-flight
// attempt's private typed input or races its callback lifetime.
func (prepared Prepared) Clear() {
	if prepared.state != nil && prepared.state.attempted.CompareAndSwap(false, true) {
		prepared.state.provision = nil
	}
}

// Prepare checks the exact previously described declaration and cleans the
// actual typed ModelForm input before any backend is opened. No hash, server
// identity generation or DB validator is invoked at this stage.
func (command Command) Prepare(ctx context.Context, fingerprint string, values []Value) (Prepared, error) {
	if err := command.valid(ctx); err != nil {
		return Prepared{}, err
	}
	if fingerprint != command.state.description.Fingerprint || len(values) != len(command.state.description.Fields) {
		return Prepared{}, failure(InvalidInput, nil)
	}
	data := make(map[string][]string, len(values))
	total := 0
	for i, value := range values {
		field := command.state.description.Fields[i]
		if value.Name != field.Name || !value.Present && len(value.Text) != 0 || len(value.Text) > field.MaxBytes || !utf8.Valid(value.Text) || bytes.IndexByte(value.Text, 0) >= 0 {
			return Prepared{}, failure(InvalidInput, nil)
		}
		total += len(value.Text)
		if total > MaxInputBytes {
			return Prepared{}, failure(InvalidInput, nil)
		}
		if value.Present {
			data[value.Name] = []string{string(value.Text)}
		}
	}
	call, err := command.state.clean(ctx, forms.NewData(data))
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{&preparedState{owner: command.state, provision: call}}, nil
}

// Provision claims this command's input once and uses the real selected-model
// owner. Acquisition/closing remain with the caller, which must preserve a
// confirmed result if a later close or response write fails.
func (command Command) Provision(ctx context.Context, backend Backend, operationID string, prepared Prepared, password string) (accountmodel.FirstAccountCreation, error) {
	if err := command.valid(ctx); err != nil {
		return accountmodel.FirstAccountCreation{}, err
	}
	if nilValue(backend) || prepared.state == nil || prepared.state.owner != command.state || !validDigest(operationID) || len(password) == 0 || len(password) > command.state.description.MaxPasswordBytes {
		return accountmodel.FirstAccountCreation{}, failure(InvalidInput, nil)
	}
	if !prepared.state.attempted.CompareAndSwap(false, true) {
		return accountmodel.FirstAccountCreation{}, failure(Consumed, nil)
	}
	call := prepared.state.provision
	prepared.state.provision = nil
	return call(ctx, backend, operationID, password)
}

func (command Command) valid(ctx context.Context) error {
	if command.state == nil {
		return failure(InvalidConfig, nil)
	}
	if nilValue(ctx) {
		return failure(InvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return failure(InvalidInput, err)
	}
	return nil
}
func nilValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	}
	return false
}
func validDigest(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == "" && strings.Trim(value, "0") != ""
}
func safeName(value string) bool {
	return len(value) > 0 && len(value) <= 128 && strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") == ""
}
func safeLabel(value string) bool {
	if len(value) == 0 || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if !unicode.IsGraphic(r) {
			return false
		}
	}
	return true
}
