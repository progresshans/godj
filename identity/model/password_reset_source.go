package model

import (
	"context"
	"errors"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/validation"
)

// PasswordResetCandidates returns a complete bounded delivery batch after its
// native read scope closes. Selection uses the declared Email/Active roles and
// the same Query AST as ordinary typed queries. No default column is inferred.
func (owner *PasswordResetter[M]) PasswordResetCandidates(ctx context.Context, email string) ([]auth.PasswordResetToken, error) {
	if err := owner.valid(ctx); err != nil {
		return nil, err
	}
	if !owner.PasswordResetEmailAvailable() {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	cleaned, diagnostics := auth.CleanPasswordResetEmail(email)
	if !diagnostics.Empty() {
		return nil, validation.Reject(diagnostics, nil)
	}
	state := owner.state
	accounts, err := db.ReadSnapshotResult(ctx, state.store.state.backend, func(work context.Context, scope db.Queryer) ([]passwordResetAccount[M], error) {
		namespace, err := state.domainIn(work, scope)
		if err != nil {
			return nil, err
		}
		store := state.store.state.credentials
		binding := store.state.binding
		active := binding.state.role.Active
		if active.Constant != nil && !*active.Constant {
			return nil, nil
		}
		predicates := []orm.Predicate[M]{orm.NewStringField[M](state.email).IExact(cleaned)}
		if active.Field != "" {
			predicates = append(predicates, orm.NewBooleanField[M](state.active).Exact(true))
		}
		query, err := binding.state.objects.Using(scope).Filter(predicates...).OrderBy(store.state.key.Asc()).Limit(auth.MaximumPasswordResetCandidates + 1)
		if err != nil {
			return nil, err
		}
		rows, err := query.All(work)
		if err != nil {
			return nil, err
		}
		// Count every database-iexact active row before usable-password and
		// Unicode filtering; never deliver a truncated prefix of matches.
		if len(rows) > auth.MaximumPasswordResetCandidates {
			return nil, managementError(CodePersistence, nil)
		}
		folded := unicode16.CaseFold(unicode16.NFKC(cleaned))
		var selected []passwordResetAccount[M]
		for _, row := range rows {
			values, err := binding.state.objects.ModelValues(row)
			if err != nil {
				return nil, err
			}
			recipient, err := state.emailValue(values)
			if err != nil {
				return nil, err
			}
			if unicode16.CaseFold(unicode16.NFKC(recipient)) != folded {
				continue
			}
			account, err := state.accountFromRow(work, scope, namespace, row)
			if err != nil {
				return nil, err
			}
			if account.credential.Principal().Authenticated() && account.credential.HasUsablePassword() {
				selected = append(selected, account)
			}
		}
		return selected, nil
	})
	if err != nil {
		return nil, accountPasswordFailure(err)
	}
	now, err := state.instant()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return nil, accountPasswordFailure(err)
	}
	tokens := make([]auth.PasswordResetToken, 0, len(accounts))
	for _, account := range accounts {
		token, err := state.config.Keys.Issue(account.observation, now.Unix())
		if err != nil {
			return nil, accountPasswordFailure(err)
		}
		tokens = append(tokens, token)
	}
	if err := ctx.Err(); err != nil {
		return nil, accountPasswordFailure(err)
	}
	return tokens, nil
}
