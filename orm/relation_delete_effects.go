package orm

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/progresshans/godj/schema/ir"
)

// RelationDeleteEffect identifies a requested root or a collected dependent.
// NullFields names all FK fields to clear, including temporary
// changes on a row that is itself deleted. Lifecycle owners give Deleted
// precedence when computing final revisions, audits and session revocation.
type RelationDeleteEffect struct {
	Model      ir.ModelIdentity
	Key        int64
	Deleted    bool
	NullFields []string
}

// RelationDeleteEffects is an immutable observation of one collected operation.
// It grants no authorization and cannot execute or replay a deletion. Effects
// are provisional until the enclosing transaction commits. A requested root
// key is not proof of existence; the composing owner checks its own current
// row, revision and authorization preconditions in the same transaction.
type RelationDeleteEffects struct{ rows []RelationDeleteEffect }

func (RelationDeleteEffects) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.RelationDeleteEffects{redacted}"))
}

// Rows returns detached rows in model/key order. A present zero or negative key
// is preserved. Multiple paths and multiple nulled fields share one row entry.
func (effects RelationDeleteEffects) Rows() []RelationDeleteEffect {
	rows := slices.Clone(effects.rows)
	for index := range rows {
		rows[index].NullFields = slices.Clone(rows[index].NullFields)
	}
	return rows
}

func collectRelationDeleteEffects(deleted []relationDeleteRow, nulls map[relationDeleteRow][]string) RelationDeleteEffects {
	rows := make(map[relationDeleteRow]RelationDeleteEffect, len(deleted)+len(nulls))
	for _, row := range deleted {
		rows[row] = RelationDeleteEffect{Model: row.model, Key: row.key, Deleted: true}
	}
	for row, fields := range nulls {
		effect := rows[row]
		effect.Model, effect.Key = row.model, row.key
		effect.NullFields = slices.Clone(fields)
		slices.Sort(effect.NullFields)
		effect.NullFields = slices.Compact(effect.NullFields)
		rows[row] = effect
	}
	effects := RelationDeleteEffects{rows: make([]RelationDeleteEffect, 0, len(rows))}
	for _, row := range rows {
		effects.rows = append(effects.rows, row)
	}
	slices.SortFunc(effects.rows, func(left, right RelationDeleteEffect) int {
		if order := cmp.Compare(left.Model.AppLabel, right.Model.AppLabel); order != 0 {
			return order
		}
		if order := cmp.Compare(left.Model.ModelName, right.Model.ModelName); order != 0 {
			return order
		}
		return cmp.Compare(left.Key, right.Key)
	})
	return effects
}
