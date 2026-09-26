package queryplan

import (
	"math"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// AppendPrefetchWindow keeps backend-specific value/path compilation in the
// caller. The rank participates in DISTINCT, matching the sliced reference
// query; deduplicating model rows before ranking changes through multiplicity.
func AppendPrefetchWindow(sql *strings.Builder, plan query.Plan, appendValue func(query.ResultExpression) error) error {
	window, ok := plan.PrefetchWindow()
	if !ok {
		return nil
	}
	sql.WriteString(", ROW_NUMBER() OVER (PARTITION BY ")
	if err := appendValue(window.Partition()); err != nil {
		return err
	}
	for i, ordering := range plan.Orderings() {
		if i == 0 {
			sql.WriteString(" ORDER BY ")
		} else {
			sql.WriteString(", ")
		}
		if err := appendValue(ordering.Expression()); err != nil {
			return err
		}
		appendDirection(sql, ordering.Direction())
	}
	sql.WriteString(`) AS "godj_prefetch_rank"`)
	return nil
}

// FinishPrefetchRows strips private ordering/rank cells without changing the
// public model/eager/owner scan order. Membership stays in the inner query;
// grouping ownership is enforced here when the two use different join scopes.
func FinishPrefetchRows(inner string, plan query.Plan, selected, hidden []query.ResultExpression, quote func(string) (string, error), parameter func(int64) string, packedMembership func([]query.Value) (string, bool)) (string, error) {
	window, ok := plan.PrefetchWindow()
	if !ok {
		return "", invalidPlan("prefetch row wrapper has no window")
	}
	wrapped, err := WrapHiddenOrderings(inner, selected, quote)
	if err != nil {
		return "", err
	}
	var sql strings.Builder
	sql.WriteString(wrapped)
	const rank = `"godj_order_source"."godj_prefetch_rank"`
	low, _ := plan.Offset()
	sql.WriteString(" WHERE " + rank + " > " + parameter(int64(low)))
	if limit, limited := plan.Limit(); limited {
		// ROW_NUMBER is an int64 on both supported backends. Saturating the
		// bound avoids integer overflow for a valid very large target limit.
		high := int64(math.MaxInt64)
		if int64(limit) <= math.MaxInt64-int64(low) {
			high = int64(low) + int64(limit)
		}
		sql.WriteString(" AND " + rank + " <= " + parameter(high))
	}
	if owner, values, late := window.OwnerFilter(); late {
		index := ExpressionIndex(selected, owner)
		if index < 0 {
			return "", invalidPlan("prefetch grouping owner is not selected")
		}
		if len(values) == 0 {
			sql.WriteString(" AND 0 = 1")
		} else if membership, packed := packedMembership(values); packed {
			sql.WriteString(` AND "godj_order_source"."c` + strconv.Itoa(index) + `"` + membership)
		} else {
			sql.WriteString(` AND "godj_order_source"."c` + strconv.Itoa(index) + `" IN (`)
			for i, value := range values {
				key, integer := value.Integer()
				if !integer || value.IsNull() {
					return "", invalidPlan("prefetch grouping owner is not an integer")
				}
				if i > 0 {
					sql.WriteString(", ")
				}
				sql.WriteString(parameter(key))
			}
			sql.WriteByte(')')
		}
	}
	for i, ordering := range plan.Orderings() {
		index := ExpressionIndex(hidden, ordering.Expression())
		if index < 0 {
			return "", invalidPlan("prefetch ordering is not selected")
		}
		if i == 0 {
			sql.WriteString(" ORDER BY ")
		} else {
			sql.WriteString(", ")
		}
		sql.WriteString(`"godj_order_source"."o` + strconv.Itoa(index) + `"`)
		appendDirection(&sql, ordering.Direction())
	}
	return sql.String(), nil
}

func appendDirection(sql *strings.Builder, direction query.Direction) {
	if direction == query.Ascending {
		sql.WriteString(" ASC")
	} else {
		sql.WriteString(" DESC")
	}
}
