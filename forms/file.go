package forms

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

type privateFile struct {
	name   string
	upload uploads.File
	clear  bool
}

// FileValue distinguishes a retained server reference, a newly received upload,
// and explicit clearing. A reference is an opaque storage name, never a URL or
// authority to read a filesystem path. Actual reads and persistence are explicit.
type FileValue struct{ state *privateFile }

func (f FileValue) Name() string {
	if f.state == nil {
		return ""
	}
	return f.state.name
}
func (f FileValue) Upload() (uploads.File, bool) {
	if f.state == nil {
		return uploads.File{}, false
	}
	return f.state.upload, f.state.upload.Valid()
}
func (f FileValue) Clear() bool                  { return f.state != nil && f.state.clear }
func (FileValue) Format(state fmt.State, _ rune) { fmt.Fprint(state, "forms.FileValue{redacted}") }

// ExistingFile constructs initial input from an application-owned storage name.
// Bind preserves it when no new file or permitted clear request is submitted.
func ExistingFile(name string) (Value, error) {
	if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return Value{}, &ConfigError{Path: "file", Code: "invalid_name"}
	}
	return Value{kind: ValueFile, fileState: &privateFile{name: name}}, nil
}
func (v Value) AsFile() (FileValue, bool) {
	return FileValue{v.fileState}, v.kind == ValueFile && v.fileState != nil
}
func (v Values) File(name string) (FileValue, bool) {
	value, ok := v.Get(name)
	if !ok {
		return FileValue{}, false
	}
	return value.AsFile()
}

// WithAllowEmptyFile accepts a received zero-byte file. It does not make a
// missing required upload valid. It applies only to FileField.
func WithAllowEmptyFile(allow bool) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.allowEmptyFile, config.hasAllowEmptyFile = allow, true })
}
func FileField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, nullable: true, widget: ClearableFileInput}
	for _, option := range options {
		if nilInterface(option) {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldFile, config)
}
func (f Field) AllowEmptyFile() bool { return f.allowEmptyFile }
func (s Spec) IsMultipart() bool {
	for _, field := range s.fields {
		if field.kind == FieldFile {
			return true
		}
	}
	return false
}
func (s SetSpec) IsMultipart() bool { return s.row.IsMultipart() }

func cleanFile(field Field, data Data, initial Value) (Value, validation.Errors) {
	fail := func(code validation.Code) (Value, validation.Errors) {
		return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
	}
	files, _ := data.rawFiles(field.name)
	if len(files) > 1 {
		return fail("multiple")
	}
	clear, code := fileClear(field, data)
	if code != "" {
		return fail(code)
	}
	if clear && len(files) != 0 {
		return fail("contradiction")
	}
	if clear {
		return Value{kind: ValueFile, fileState: &privateFile{clear: true}}, validation.Errors{}
	}
	if len(files) == 0 {
		if !initial.IsNull() {
			return initial, validation.Errors{}
		}
		if field.required {
			return fail("required")
		}
		return Null(), validation.Errors{}
	}
	file := files[0]
	if !file.Valid() {
		return fail("invalid")
	}
	if field.maxLength > 0 && utf8.RuneCountInString(file.Name()) > field.maxLength {
		return fail("max_length")
	}
	if !field.allowEmptyFile && file.Size() == 0 {
		return fail("empty")
	}
	value := Value{kind: ValueFile, fileState: &privateFile{name: file.Name(), upload: file}}
	return value, runFieldValidators(field, value)
}
func runFieldValidators(field Field, value Value) validation.Errors {
	var failures []validation.Errors
	for _, validator := range field.validators {
		failures = append(failures, validator.ValidateField(value))
	}
	return validation.Join(failures...)
}
func fileClear(field Field, data Data) (bool, validation.Code) {
	if field.required || field.widget != ClearableFileInput {
		return false, ""
	}
	values, _ := data.raw(field.name + "-clear")
	if len(values) > 1 {
		return false, "multiple"
	}
	if len(values) == 0 {
		return false, ""
	}
	switch strings.ToLower(strings.TrimSpace(values[0])) {
	case "", "0", "false", "off", "no":
		return false, ""
	case "1", "true", "on", "yes":
		return true, ""
	default:
		return false, "invalid"
	}
}
func fileChanged(field Field, data Data) bool {
	files, _ := data.rawFiles(field.name)
	clear, code := fileClear(field, data)
	return len(files) != 0 || clear || code != ""
}

func (d Data) rawFiles(name string) ([]uploads.File, bool) {
	if d.state == nil {
		return nil, false
	}
	files, ok := d.state.files[name]
	return files, ok
}
func (d Data) Files(name string) ([]uploads.File, bool) {
	files, ok := d.rawFiles(name)
	return append([]uploads.File(nil), files...), ok
}

// NewDataWithFiles snapshots containers without reading file content. Upload
// capabilities retain their owner's lifetime; binding does not extend it.
func NewDataWithFiles(values map[string][]string, files map[string][]uploads.File) Data {
	cloned := make(map[string][]string, len(values))
	for name, submitted := range values {
		cloned[name] = append([]string(nil), submitted...)
	}
	fileValues := make(map[string][]uploads.File, len(files))
	for name, submitted := range files {
		fileValues[name] = append([]uploads.File(nil), submitted...)
	}
	return Data{state: &submittedData{values: cloned, files: fileValues}}
}
