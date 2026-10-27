package firstaccount

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/internal/callback"
)

// Config explicitly bounds file input and identifies its report owner. A
// command with upload fields requires positive limits and ReportFiles; other
// commands use the zero configuration. Limits are part of Description's digest.
type Config struct {
	MaxFileBytes, MaxFilesBytes int
	ReportFiles                 func(context.Context, FileReport) error
}

// Upload is one received local file. Name is a basename, never a local path or
// storage reference. The caller owns Content; Prepare snapshots it and expires
// the snapshot after one invocation or Clear. An empty file is still present.
type Upload struct {
	Name    string
	Content []byte
}

func (Upload) Format(state fmt.State, _ rune) { fmt.Fprint(state, "firstaccount.Upload{redacted}") }
func (Upload) MarshalJSON() ([]byte, error)   { return []byte(`"firstaccount.Upload{redacted}"`), nil }

// ValidUploadName checks only transport metadata. The common uploads package
// subsequently applies its canonical client-basename sanitation once.
func ValidUploadName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= MaxUploadNameBytes && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 255 && !strings.ContainsAny(name, `/\`) && !strings.ContainsFunc(name, func(r rune) bool { return !unicode.IsPrint(r) })
}

// Clear releases the caller-owned mutable bytes, including an explicit empty
// upload. It does not affect a successfully detached preparation.
func (value *Value) Clear() {
	if value == nil {
		return
	}
	clear(value.Text)
	if value.Upload != nil {
		clear(value.Upload.Content)
	}
	*value = Value{}
}

// ValidateValues checks presence, kind, exact field order and both aggregate
// budgets without reading a file or acquiring a backend. Typed Form cleaning
// remains responsible for required/default/image/model meaning.
func ValidateValues(description Description, values []Value) error {
	if ValidateDescription(description) != nil || len(values) != len(description.Fields) {
		return failure(InvalidInput, nil)
	}
	textBytes, fileBytes := 0, 0
	for i, value := range values {
		field := description.Fields[i]
		if value.Name != field.Name || !value.Present && (len(value.Text) != 0 || value.Upload != nil) {
			return failure(InvalidInput, nil)
		}
		if field.Upload {
			if len(value.Text) != 0 || value.Present != (value.Upload != nil) {
				return failure(InvalidInput, nil)
			}
			if value.Upload != nil {
				if !ValidUploadName(value.Upload.Name) || len(value.Upload.Content) > description.MaxFileBytes || len(value.Upload.Content) > description.MaxFilesBytes-fileBytes {
					return failure(InvalidInput, nil)
				}
				fileBytes += len(value.Upload.Content)
			}
		} else {
			if value.Upload != nil || len(value.Text) > field.MaxBytes || len(value.Text) > MaxInputBytes-textBytes || !utf8.Valid(value.Text) || bytes.IndexByte(value.Text, 0) >= 0 {
				return failure(InvalidInput, nil)
			}
			textBytes += len(value.Text)
		}
	}
	return nil
}

func (state *preparedState) closeUploads() {
	for _, buffer := range state.uploads {
		_ = buffer.Close()
	}
	state.uploads = nil
}

// Result keeps the confirmed database receipt distinct from file publications
// and report delivery. Err from Provision describes the database attempt;
// ReportErr never erases a confirmed creation or an earlier publication.
type Result struct {
	accountmodel.FirstAccountCreation
	files     []formmodel.FilePublication
	reportErr error
}

func (result Result) Files() []formmodel.FilePublication { return slices.Clone(result.files) }
func (result Result) ReportErr() error                   { return result.reportErr }
func (Result) Format(state fmt.State, _ rune)            { fmt.Fprint(state, "firstaccount.Result{redacted}") }
func (Result) MarshalJSON() ([]byte, error)              { return []byte(`"firstaccount.Result{redacted}"`), nil }

// FileReport is delivered exactly once when publication was attempted, even
// after a later validation/hash/transaction failure. The application must keep
// these outcomes with OperationID for reconciliation. It grants no retry or
// deletion authority. Accessors detach containers; normal formatting is private.
type FileReport struct {
	operation string
	creation  accountmodel.FirstAccountCreation
	files     []formmodel.FilePublication
	err       error
}

func (report FileReport) OperationID() string { return report.operation }
func (report FileReport) Creation() accountmodel.FirstAccountCreation {
	return cloneCreation(report.creation)
}
func (report FileReport) Files() []formmodel.FilePublication { return slices.Clone(report.files) }
func (report FileReport) Err() error                         { return report.err }
func (FileReport) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "firstaccount.FileReport{redacted}")
}
func (FileReport) MarshalJSON() ([]byte, error) {
	return []byte(`"firstaccount.FileReport{redacted}"`), nil
}

var reportFailed = errors.New("firstaccount: file report failed")

func (command Command) reportFiles(ctx context.Context, operation string, created accountmodel.FirstAccountCreation, files []formmodel.FilePublication, err error) error {
	report := FileReport{operation: operation, creation: cloneCreation(created), files: slices.Clone(files), err: err}
	_, failed := callback.Invoke(func() (struct{}, error) {
		if command.state.config.ReportFiles == nil {
			return struct{}{}, reportFailed
		}
		return struct{}{}, command.state.config.ReportFiles(ctx, report)
	}, reportFailed)
	if failed != nil {
		return failure(ReportFailed, failed)
	}
	return nil
}

func cloneCreation(created accountmodel.FirstAccountCreation) accountmodel.FirstAccountCreation {
	created.Account.ChangedFields = slices.Clone(created.Account.ChangedFields)
	return created
}
