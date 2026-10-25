package identity

import (
	"context"
	"errors"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

var _ auth.PasswordResetMailSource = (*PasswordResetter)(nil)

// PasswordResetCandidates keeps account selection and signing observations in
// this persistence owner. The mail consumer receives only detached delivery
// material after the complete read scope has closed successfully.
func (resetter *PasswordResetter) PasswordResetCandidates(ctx context.Context, email string) ([]auth.PasswordResetToken, error) {
	if nilIdentityValue(ctx) {
		return nil, managementError(CodeInvalidInput, "context", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, managementWriteFailure(err)
	}
	if resetter == nil || resetter.state == nil {
		return nil, managementError(CodeInvalidConfig, "password_reset", nil)
	}
	cleaned, failures := auth.CleanPasswordResetEmail(email)
	if !failures.Empty() {
		return nil, validation.Reject(failures, nil)
	}
	accounts, err := db.ReadSnapshotResult(ctx, resetter.state.backend, func(work context.Context, reader db.Queryer) ([]Account, error) {
		query, err := models.UserObjects.Using(reader).Filter(models.UserFields.Active.Exact(true), models.UserFields.Email.IExact(cleaned)).OrderBy(models.UserFields.ID.Asc()).Limit(auth.MaximumPasswordResetCandidates + 1)
		if err != nil {
			return nil, err
		}
		rows, err := query.All(work)
		if err != nil {
			return nil, err
		}
		if len(rows) > auth.MaximumPasswordResetCandidates {
			return nil, managementError(CodePersistence, "password_reset_candidates", nil)
		}
		folded := unicode16.CaseFold(unicode16.NFKC(cleaned))
		var accounts []Account
		for _, row := range rows {
			if unicode16.CaseFold(unicode16.NFKC(row.Email)) != folded {
				continue
			}
			account, err := resetter.state.directory.accountFromRow(work, reader, row)
			if err != nil {
				return nil, err
			}
			if account.value().credential.Principal().Authenticated() && account.HasUsablePassword() {
				accounts = append(accounts, account)
			}
		}
		return accounts, nil
	})
	if err != nil {
		return nil, managementError(CodePersistence, "password_reset_candidates", err)
	}
	now, err := resetter.instant()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return nil, managementError(CodePersistence, "password_reset_request", err)
	}
	tokens := make([]auth.PasswordResetToken, 0, len(accounts))
	for _, account := range accounts {
		observation, err := passwordResetObservation(account)
		if err != nil {
			return nil, managementWriteFailure(err)
		}
		token, err := resetter.state.keys.Issue(observation, now)
		if err != nil {
			return nil, managementWriteFailure(err)
		}
		tokens = append(tokens, token)
	}
	if err := ctx.Err(); err != nil {
		return nil, managementWriteFailure(err)
	}
	return tokens, nil
}
