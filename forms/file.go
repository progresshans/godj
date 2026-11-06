package forms

import (
	"context"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

type privateFile struct {
	name   string
	upload uploads.File
	clear  bool
	image  uploads.ImageInfo
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
func (f FileValue) Clear() bool { return f.state != nil && f.state.clear }

// Image reports verified metadata for a new ImageField upload or an explicitly
// inspected image choice. An ordinary retained reference has no such metadata.
func (f FileValue) Image() (uploads.ImageInfo, bool) {
	if f.state == nil {
		return uploads.ImageInfo{}, false
	}
	return f.state.image, f.state.image.Valid()
}
func (FileValue) Format(state fmt.State, _ rune) { fmt.Fprint(state, "forms.FileValue{redacted}") }

// ExistingFile constructs initial input from an application-owned storage name.
// The empty name represents an empty stored reference, distinct from SQL NULL.
// Bind preserves it when no new file or permitted clear request is submitted.
func ExistingFile(name string) (Value, error) {
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
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
// missing required upload valid. An ImageField must still decode as an image.
func WithAllowEmptyFile(allow bool) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.allowEmptyFile, config.hasAllowEmptyFile = allow, true })
}
func FileField(name string, options ...FieldOption) (Field, error) {
	return newFileField(name, FieldFile, options...)
}

// ImageField verifies supported PNG/APNG/JPEG/BMP/DIB/WebP, GIF frames and TIFF
// pages before pure validators run. Bind performs cancellable upload I/O.
func ImageField(name string, options ...FieldOption) (Field, error) {
	return newFileField(name, FieldImage, options...)
}

func WithImageLimits(limits uploads.ImageLimits) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.imageLimits, config.hasImageLimits = limits, true })
}

// ImageChoiceInspector is an explicit I/O capability for server-declared image
// names. It must inspect the authorized name completely within the given limits,
// close any acquired reader, preserve errors and be safe for concurrent use.
// storage.NewImageInspector supplies the standard storage-backed adapter.
type ImageChoiceInspector func(context.Context, string, uploads.ImageLimits) (uploads.ImageInfo, error)

// WithImageChoiceInspector is required for an ImageField with choices. Binding
// inspects a valid, nonempty submitted choice before pure validators run. It
// does not inspect excluded/readonly fields, empty input or unbound initial data.
func WithImageChoiceInspector(inspector ImageChoiceInspector) FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.inspectImageChoice, config.hasImageInspector = inspector, true
	})
}

func (f Field) ImageLimits() (uploads.ImageLimits, bool) { return f.imageLimits, f.kind == FieldImage }
func (f Field) IsFile() bool                             { return fileFieldKind(f.kind) }

// AcceptsUpload distinguishes an upload control from a stored-name choice.
// Both use FileValue, but only an upload control needs multipart transport.
func (f Field) AcceptsUpload() bool     { return f.IsFile() && f.choices == nil }
func fileFieldKind(kind FieldKind) bool { return kind == FieldFile || kind == FieldImage }

func newFileField(name string, kind FieldKind, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: ClearableFileInput}
	for _, option := range options {
		if nilInterface(option) {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	if config.choices == nil {
		config.nullable = true
	}
	return makeField(name, kind, config)
}
func (f Field) AllowEmptyFile() bool { return f.allowEmptyFile }
func (s Spec) IsMultipart() bool {
	for _, field := range s.fields {
		if field.AcceptsUpload() {
			return true
		}
	}
	return false
}
func (s SetSpec) IsMultipart() bool { return s.row.IsMultipart() }

func cleanFile(ctx context.Context, field Field, data Data, initial Value) (Value, validation.Errors, error) {
	if field.choices != nil {
		return cleanFileChoice(ctx, field, data)
	}
	fail := func(code validation.Code) (Value, validation.Errors, error) {
		return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code)), nil
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
		return Value{kind: ValueFile, fileState: &privateFile{clear: true}}, validation.Errors{}, nil
	}
	if len(files) == 0 {
		if file, ok := initial.AsFile(); ok && (file.Name() != "" || !field.required) {
			return initial, validation.Errors{}, nil
		}
		if field.required {
			return fail("required")
		}
		return Null(), validation.Errors{}, nil
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
	var extensionErrors validation.Errors
	if field.kind == FieldImage {
		info, err := uploads.InspectImage(ctx, file, field.imageLimits)
		if err != nil {
			if failure, ok := err.(*uploads.Error); ok {
				switch failure.Code {
				case "invalid_image", "unsupported_image":
					return fail("invalid_image")
				case "image_bytes", "image_pixels", "image_frames":
					return fail(validation.Code(failure.Code))
				}
			}
			return Null(), validation.Errors{}, err
		}
		// As in Django, extension validation follows content verification. The
		// extension need not match the detected format; MIME always comes from
		// the verified content. Client ContentType remains untrusted metadata.
		switch strings.ToLower(path.Ext(file.Name())) {
		case ".png", ".apng", ".jpg", ".jpeg", ".jpe", ".gif", ".webp", ".bmp", ".dib", ".tif", ".tiff":
		default:
			extensionErrors = validation.NewErrors(validation.New(validation.Field(field.name), "invalid_extension"))
		}
		value.fileState.image = info
	}
	// Extension validation and user validators are peers. Continue running
	// pure validators even when an extension is rejected, preserving their
	// ordered diagnostics and verified metadata, as Django run_validators does.
	return value, validation.Join(extensionErrors, runFieldValidators(field, value)), nil
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
func cleanFileChoice(ctx context.Context, field Field, data Data) (Value, validation.Errors, error) {
	fail := func(code validation.Code) (Value, validation.Errors, error) {
		return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code)), nil
	}
	submitted, _ := data.raw(field.name)
	if len(submitted) > 1 {
		return fail("multiple")
	}
	raw := ""
	if len(submitted) == 1 {
		raw = submitted[0]
	}
	if raw == "" {
		if field.required {
			return fail("required")
		}
		if field.nullable {
			return Null(), validation.Errors{}, nil
		}
		return Value{kind: ValueFile, fileState: &privateFile{clear: true}}, validation.Errors{}, nil
	}
	if _, code := cleanChoice(field, raw); code != "" {
		return fail(code)
	}
	value := Value{kind: ValueFile, fileState: &privateFile{name: raw}}
	if field.kind == FieldImage {
		info, err := field.inspectImageChoice(ctx, raw, field.imageLimits)
		if err != nil {
			return Null(), validation.Errors{}, err
		}
		if err := ctx.Err(); err != nil {
			return Null(), validation.Errors{}, err
		}
		if !info.Valid() {
			return Null(), validation.Errors{}, &ConfigError{Path: "fields." + field.name + ".image_choice_inspector", Code: "invalid_result"}
		}
		value.fileState.image = info
	}
	return value, runFieldValidators(field, value), nil
}

func fileChanged(field Field, data Data, initial Value) bool {
	if field.choices != nil {
		submitted, _ := data.raw(field.name)
		if len(submitted) > 1 {
			return true
		}
		raw := ""
		if len(submitted) == 1 {
			raw = submitted[0]
		}
		file, _ := initial.AsFile()
		return raw != file.Name()
	}
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
