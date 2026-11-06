package identityadmin

import (
	"fmt"
	"slices"

	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
)

// AccountFileEditReport separates attempted storage publication from confirmed
// account metadata. Err nil confirms the account operation; non-nil leaves
// Change zero even when one or more publications were confirmed. A native
// outcome-unknown error must be reconciled independently from storage outcome.
// This report never extends request, upload or database scope lifetimes.
type AccountFileEditReport struct{ state *accountFileEditReport }
type accountFileEditReport struct {
	id           int64
	change       accountmodel.AccountChange
	publications []formmodel.FilePublication
	err          error
}

func newAccountFileEditReport(id int64, change accountmodel.AccountChange, publications []formmodel.FilePublication, err error) AccountFileEditReport {
	change.ChangedFields = slices.Clone(change.ChangedFields)
	return AccountFileEditReport{state: &accountFileEditReport{id, change, slices.Clone(publications), err}}
}

func (report AccountFileEditReport) ID() int64 {
	if report.state == nil {
		return 0
	}
	return report.state.id
}
func (report AccountFileEditReport) Change() accountmodel.AccountChange {
	if report.state == nil {
		return accountmodel.AccountChange{}
	}
	change := report.state.change
	change.ChangedFields = slices.Clone(change.ChangedFields)
	return change
}
func (report AccountFileEditReport) Publications() []formmodel.FilePublication {
	if report.state == nil {
		return nil
	}
	return slices.Clone(report.state.publications)
}
func (report AccountFileEditReport) Err() error {
	if report.state == nil {
		return &accountmodel.Error{Code: accountmodel.CodeInvalidInput}
	}
	return report.state.err
}
func (AccountFileEditReport) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "identityadmin.AccountFileEditReport{redacted}")
}

// AccountFileCreationReport separates file publication from a confirmed new
// account. A failed/unknown creation has a zero Creation, while Publications
// retains every attempted file. Reports own metadata, not upload lifetimes.
type AccountFileCreationReport struct{ state *accountFileCreationReport }
type accountFileCreationReport struct {
	creation     accountmodel.Creation
	publications []formmodel.FilePublication
	err          error
}

func newAccountFileCreationReport(creation accountmodel.Creation, publications []formmodel.FilePublication, err error) AccountFileCreationReport {
	creation.ChangedFields = slices.Clone(creation.ChangedFields)
	return AccountFileCreationReport{state: &accountFileCreationReport{creation, slices.Clone(publications), err}}
}
func (report AccountFileCreationReport) Creation() accountmodel.Creation {
	if report.state == nil {
		return accountmodel.Creation{}
	}
	creation := report.state.creation
	creation.ChangedFields = slices.Clone(creation.ChangedFields)
	return creation
}
func (report AccountFileCreationReport) Publications() []formmodel.FilePublication {
	if report.state == nil {
		return nil
	}
	return slices.Clone(report.state.publications)
}
func (report AccountFileCreationReport) Err() error {
	if report.state == nil {
		return &accountmodel.Error{Code: accountmodel.CodeInvalidInput}
	}
	return report.state.err
}
func (AccountFileCreationReport) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "identityadmin.AccountFileCreationReport{redacted}")
}
