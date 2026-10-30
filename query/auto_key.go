package query

// MaximumAutoKeyReservations bounds the result of one native allocation. A
// larger owner may request multiple batches in its one transaction scope.
const MaximumAutoKeyReservations = 65536

// AutoKeyPlan requests database-generated integer keys from the declared
// table's own AutoField allocator. It contains no guessed starting key, row
// values or SQL dialect. The backend verifies the actual primary key and its
// native allocator before reserving anything. This plan does not insert rows.
type AutoKeyPlan struct {
	table string
	key   FieldRef
	count int
}

func NewAutoKeyPlan(table string, key FieldRef, count int) (AutoKeyPlan, error) {
	plan := AutoKeyPlan{table: table, key: key, count: count}
	if err := plan.Validate(); err != nil {
		return AutoKeyPlan{}, err
	}
	return plan, nil
}

func (plan AutoKeyPlan) Table() string                { return plan.table }
func (plan AutoKeyPlan) Key() FieldRef                { return plan.key }
func (plan AutoKeyPlan) Count() int                   { return plan.count }
func (plan AutoKeyPlan) Equal(other AutoKeyPlan) bool { return plan == other }

func (plan AutoKeyPlan) Validate() error {
	if plan.table == "" || !plan.key.ValidType() || plan.key.Kind() != FieldInteger || plan.key.Nullable() || plan.key.Name() == "" || plan.key.Column() == "" {
		return invalidPlanError("auto-key reservation requires a table and a non-null integer primary key")
	}
	if plan.count < 1 || plan.count > MaximumAutoKeyReservations {
		return invalidPlanError("auto-key reservation count is outside its positive bound")
	}
	return nil
}
