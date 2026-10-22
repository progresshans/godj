package crud

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

type Page[M any] struct {
	Items  []M
	Total  int64
	Offset int
	Limit  int
}

type ListOptions[M any] struct {
	Filter   []orm.Predicate[M]
	Ordering []orm.Ordering[M]
	Offset   int
	Limit    int
}

func (resource Resource[M]) List(ctx context.Context, backend db.Queryer, options ListOptions[M]) (Page[M], error) {
	if err := resource.check(ctx, backend); err != nil {
		return Page[M]{}, err
	}
	rows := resource.Query(backend).Filter(options.Filter...)
	if len(options.Ordering) == 0 {
		rows = rows.OrderBy(resource.key.Asc())
	} else {
		rows = rows.OrderBy(options.Ordering...)
	}
	return ListQuery(ctx, rows, options.Offset, options.Limit)
}

// PagedQuery covers both ordinary and generated eager/prefetch queries without
// unwrapping them or replacing their backend, scope or collection ownership.
type PagedQuery[Q, M any] interface {
	Count(context.Context) (int64, error)
	Offset(int) (Q, error)
	Limit(int) (Q, error)
	All(context.Context) ([]M, error)
}

// ListQuery counts the complete scoped query and loads one ordered page.
// These are two reads, not a promise of one database snapshot. Use a caller's
// snapshot session when count/rows must observe the same snapshot. The caller
// owns the presentation's maximum size and deterministic ordering policy.
func ListQuery[M any, Q PagedQuery[Q, M]](ctx context.Context, rows Q, offset, limit int) (Page[M], error) {
	var zero Page[M]
	if isNil(ctx) || offset < 0 || limit <= 0 {
		return zero, invalid("list requires context, a nonnegative offset and a positive limit")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	page, err := rows.Offset(offset)
	if err != nil {
		return zero, err
	}
	page, err = page.Limit(limit)
	if err != nil {
		return zero, err
	}
	total, err := rows.Count(ctx)
	if err != nil {
		return zero, err
	}
	items, err := page.All(ctx)
	if err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return Page[M]{Items: items, Total: total, Offset: offset, Limit: limit}, nil
}
