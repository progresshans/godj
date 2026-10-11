package postgres

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func validateRowLockPlan(plan query.Plan) error {
	if err := plan.ValidateRowLock(); err != nil {
		return err
	}
	if _, present := plan.RowLock(); !present {
		return nil
	}
	_, window := plan.PrefetchWindow()
	if plan.Distinct() || window {
		return unsupportedResultShape("PostgreSQL row locks cannot combine with DISTINCT or windowed prefetch")
	}
	return nil
}

func appendRowLock(statement *strings.Builder, plan query.Plan, rootAlias string, joins map[queryplan.RelationKey]queryplan.Join) error {
	lock, present := plan.RowLock()
	if !present {
		return nil
	}
	targets := lock.Targets()
	aliases := make([]string, 0, len(targets))
	if len(targets) == 0 {
		for _, joined := range joins {
			if joined.LeftOuter {
				return unsupportedResultShape("PostgreSQL cannot lock the nullable side of an outer join")
			}
		}
	}
	for _, target := range targets {
		alias := rootAlias
		if target.Self() {
			if alias == "" {
				alias = plan.Table()
			}
		} else {
			joined, found := joins[queryplan.KeyForPath(target.Hops())]
			if !found {
				return invalidPlan("row lock target is absent from the SELECT scope")
			}
			if joined.LeftOuter {
				return unsupportedResultShape("PostgreSQL cannot lock the nullable side of an outer join")
			}
			alias = joined.Alias
		}
		quoted, err := quoteIdentifier(alias)
		if err != nil {
			return err
		}
		aliases = append(aliases, quoted)
	}
	if lock.Strength() == query.LockForNoKeyUpdate {
		statement.WriteString(" FOR NO KEY UPDATE")
	} else {
		statement.WriteString(" FOR UPDATE")
	}
	if len(aliases) != 0 {
		statement.WriteString(" OF ")
		statement.WriteString(strings.Join(aliases, ", "))
	}
	switch lock.WaitPolicy() {
	case query.LockNoWait:
		statement.WriteString(" NOWAIT")
	case query.LockSkipLocked:
		statement.WriteString(" SKIP LOCKED")
	}
	return nil
}

func validateRowLockTransaction(plan query.Plan, transaction, readOnly bool) error {
	if _, present := plan.RowLock(); !present || plan.EmptyResult() {
		return nil
	}
	if !transaction || readOnly {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeTransactionRequired, Detail: "PostgreSQL row locking requires a live writable transaction"}
	}
	return nil
}
