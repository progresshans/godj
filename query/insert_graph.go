package query

import "slices"

// MaximumInsertGraphRows bounds one indivisible insertion graph. A cycle may
// not be split into independent statements to work around a backend limit.
const MaximumInsertGraphRows = 256

// InsertGraphPlan contains complete scalar rows with explicit integer keys.
// A caller can reserve native keys first, bind every reference, then submit a
// cycle without temporary NULLs or weaker foreign-key constraints. It does not
// allocate keys, authorize rows, or replace the owner's typed validation.
// Each native INSERT must create exactly its requested key; conflict handling
// and updates of existing rows are deliberately absent.
type InsertGraphPlan struct {
	inserts []InsertPlan
}

func NewInsertGraphPlan(inserts []InsertPlan) (InsertGraphPlan, error) {
	plan := InsertGraphPlan{inserts: inserts}
	if err := plan.Validate(); err != nil {
		return InsertGraphPlan{}, err
	}
	// InsertPlan already owns its assignments and exposes only copies.
	plan.inserts = slices.Clone(inserts)
	return plan, nil
}

func (plan InsertGraphPlan) Inserts() []InsertPlan { return slices.Clone(plan.inserts) }
func (plan InsertGraphPlan) RowCount() int         { return len(plan.inserts) }
func (plan InsertGraphPlan) ValueCount() int {
	count := 0
	for _, insert := range plan.inserts {
		count += len(insert.assignments)
	}
	return count
}

func (plan InsertGraphPlan) Equal(other InsertGraphPlan) bool {
	return slices.EqualFunc(plan.inserts, other.inserts, InsertPlan.Equal)
}

func (plan InsertGraphPlan) Validate() error {
	if len(plan.inserts) == 0 || len(plan.inserts) > MaximumInsertGraphRows {
		return invalidPlanError("insert graph must contain a bounded nonempty set of rows")
	}
	values := 0
	tableKeys := make(map[string]FieldRef)
	type identity struct {
		table string
		key   int64
	}
	seen := make(map[identity]bool, len(plan.inserts))
	for _, insert := range plan.inserts {
		key, present := insert.ReturningKey()
		if !present || !key.ValidType() || key.Kind() != FieldInteger || key.Nullable() {
			return invalidPlanError("insert graph requires a non-null integer returning key for every row")
		}
		if len(insert.assignments) > MaximumBulkValues-values {
			return invalidPlanError("insert graph exceeds its total value bound")
		}
		values += len(insert.assignments)
		fields := make([]FieldRef, len(insert.assignments))
		row := make([]Value, len(insert.assignments))
		keyFound := false
		var keyValue int64
		for index, assignment := range insert.assignments {
			fields[index], row[index] = assignment.field, assignment.value
			if assignment.field == key {
				var integer bool
				keyValue, integer = assignment.value.Integer()
				if !integer {
					return invalidPlanError("insert graph key must be an explicit integer value")
				}
				keyFound = true
			}
		}
		shape := BulkInsertPlan{table: insert.table, fields: fields, rows: [][]Value{row}, key: key}
		if err := shape.Validate(); err != nil {
			return err
		}
		if !keyFound {
			return invalidPlanError("insert graph cannot infer an omitted key")
		}
		if previous, exists := tableKeys[insert.table]; exists && previous != key {
			return invalidPlanError("insert graph disagrees on a table's primary-key metadata")
		}
		tableKeys[insert.table] = key
		id := identity{table: insert.table, key: keyValue}
		if seen[id] {
			return invalidPlanError("insert graph repeats a table and primary key")
		}
		seen[id] = true
	}
	return nil
}
