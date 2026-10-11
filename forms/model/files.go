package model

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
)

// fileReferenceValue accepts a server-owned initial reference, never a new
// upload or clear command. Model candidates use names; form inputs use files.
func fileReferenceValue(value forms.Value) (forms.Value, error) {
	if value.IsNull() || value.Kind() == forms.ValueString {
		return value, nil
	}
	file, ok := value.AsFile()
	_, pending := file.Upload()
	_, inspected := file.Image()
	if !ok || pending || inspected || file.Clear() {
		return forms.Value{}, &Error{Path: "initial.file", Code: "reference_required"}
	}
	return forms.String(file.Name()), nil
}

func fileDefaultValue(field ir.Field) (forms.Value, error) {
	if !field.Kind.IsFile() || field.PrimaryKey || field.Relation != nil || field.Decimal != nil || field.MaxLength <= 0 {
		return forms.Value{}, &Error{Code: "invalid_file_metadata"}
	}
	if err := ir.ValidateChoices(field); err != nil {
		return forms.Value{}, &Error{Code: "invalid_choices"}
	}
	if field.Default == nil || field.Default.Kind != ir.ScalarString {
		return forms.Value{}, &Error{Code: "default_type_mismatch"}
	}
	if utf8.RuneCountInString(field.Default.String) > field.MaxLength {
		return forms.Value{}, &Error{Code: "default_max_length"}
	}
	return forms.ExistingFile(field.Default.String)
}

func fileInitialValue(value forms.Value) (forms.Value, error) {
	if value.IsNull() {
		return value, nil
	}
	name, ok := value.AsString()
	if !ok {
		return forms.Value{}, &Error{Path: "initial.file", Code: "type_mismatch"}
	}
	return forms.ExistingFile(name)
}

type pendingFile struct {
	field  ir.Field
	upload uploads.File
}

// FileSaver chooses the authorized storage and proposed name for one pending
// model upload. Name must be pure; it receives a detached model with the other
// prepared values and the old/default file references. It must not publish the
// upload itself. Storage receives the model's complete-name length limit.
type FileSaver[M any] struct {
	Field   string
	Backend storage.Backend
	Name    func(M, uploads.File) (string, error)
}

func (FileSaver[M]) Format(s fmt.State, _ rune) { fmt.Fprint(s, "model.FileSaver{redacted}") }

// FilePublication records one attempted file publication, including partial
// and uncertain failures. It does not attest to a database write or commit.
// Preserve these results when a later file or database operation fails. Neither
// an old reference nor Info is authority for automatic compensation/deletion.
type FilePublication struct {
	field string
	info  storage.Info
	err   error
}

func (p FilePublication) Field() string      { return p.field }
func (p FilePublication) Info() storage.Info { return p.info }
func (p FilePublication) Err() error         { return p.err }
func (p FilePublication) Outcome() storage.Outcome {
	if p.err == nil && p.info.Valid() {
		return storage.Published
	}
	var failure *storage.Error
	if errors.As(p.err, &failure) && failure != nil {
		return failure.Outcome
	}
	return storage.Uncertain
}
func (FilePublication) Format(s fmt.State, _ rune) { fmt.Fprint(s, "model.FilePublication{redacted}") }

// PendingFiles returns model fields whose uploads still need explicit storage.
// Command-only file inputs are not model fields and remain in Input.
func (prepared PreparedInstance[M]) PendingFiles() []string {
	result := make([]string, len(prepared.files))
	for i, file := range prepared.files {
		result[i] = file.field.Name
	}
	return result
}

func (prepared PreparedInstance[M]) requireStoredFiles() error {
	if len(prepared.files) != 0 {
		return &Error{Path: "files." + prepared.files[0].field.Name, Code: "upload_pending"}
	}
	return nil
}

// SaveFiles publishes every pending model upload in declaration order, then
// returns a new preparation containing the actual storage names. It performs
// no database I/O and never deletes an old or newly published file. Call Model
// and Save on the returned preparation in the application's authorized write
// scope; only that scope's terminal outcome can establish a database commit.
//
// All bindings and pure names are checked before the first publication. On an
// error the returned preparation is invalid, but results retain all attempted
// publications, including the failing operation. A retry is not idempotent:
// reconcile those results first. The original preparation and uploads retain
// their existing lifetimes and are not consumed, mutated or silently prolonged.
func (prepared PreparedInstance[M]) SaveFiles(ctx context.Context, savers ...FileSaver[M]) (PreparedInstance[M], []FilePublication, error) {
	operations, err := prepared.fileSaves(ctx, savers)
	if err != nil {
		return PreparedInstance[M]{}, nil, err
	}
	return prepared.saveFiles(ctx, operations)
}

type fileSave[M any] struct {
	file  pendingFile
	saver FileSaver[M]
	name  string
}

func (prepared PreparedInstance[M]) fileSaves(ctx context.Context, savers []FileSaver[M]) ([]fileSave[M], error) {
	if nilSaveValue(ctx) {
		return nil, &Error{Path: "context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := prepared.manager.Metadata(); err != nil {
		return nil, err
	}
	pending := make(map[string]bool, len(prepared.files))
	for _, file := range prepared.files {
		pending[file.field.Name] = true
	}
	byName := make(map[string]FileSaver[M], len(savers))
	for _, saver := range savers {
		path := "files." + saver.Field
		if _, found := byName[saver.Field]; found {
			return nil, &Error{Path: path, Code: "duplicate_saver"}
		}
		if !pending[saver.Field] {
			return nil, &Error{Path: path, Code: "unselected_saver"}
		}
		if nilSaveValue(saver.Backend) || saver.Name == nil {
			return nil, &Error{Path: path, Code: "invalid_saver"}
		}
		byName[saver.Field] = saver
	}
	// Validate completeness before invoking any caller callback.
	for _, file := range prepared.files {
		if _, found := byName[file.field.Name]; !found {
			return nil, &Error{Path: "files." + file.field.Name, Code: "missing_saver"}
		}
	}
	operations := make([]fileSave[M], len(prepared.files))
	for i, file := range prepared.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		instance, err := prepared.manager.ApplyValues(prepared.value, nil)
		if err != nil {
			return nil, err
		}
		name, err := byName[file.field.Name].Name(instance, file.upload)
		if err != nil {
			return nil, err
		}
		// Validate the backend-independent name before any file is published.
		// MaxLength is applied by storage, which may shorten a collision name.
		if _, err := storage.NewInfo(name, file.upload.Size()); err != nil {
			return nil, err
		}
		operations[i] = fileSave[M]{file: file, saver: byName[file.field.Name], name: name}
	}
	return operations, nil
}

func (prepared PreparedInstance[M]) saveFiles(ctx context.Context, operations []fileSave[M]) (PreparedInstance[M], []FilePublication, error) {
	values := make(map[string]query.Value, len(operations))
	results := make([]FilePublication, 0, len(operations))
	for _, operation := range operations {
		file := operation.file
		info, err := storage.SaveUpload(ctx, operation.saver.Backend, operation.name, file.upload, storage.SaveOptions{MaxLength: file.field.MaxLength})
		if err == nil && utf8.RuneCountInString(info.Name()) > file.field.MaxLength {
			err = &storage.Error{Code: "invalid_result", Outcome: storage.Uncertain}
		}
		results = append(results, FilePublication{field: file.field.Name, info: info, err: err})
		if err != nil {
			return PreparedInstance[M]{}, results, err
		}
		values[file.field.Name] = query.String(info.Name())
	}
	value, err := prepared.manager.ApplyValues(prepared.value, values)
	if err != nil {
		return PreparedInstance[M]{}, results, err
	}
	prepared.value, prepared.files = value, nil
	return prepared, results, nil
}
