package orm

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// ResultPage contains a total and scalar rows from one statement snapshot.
// Page is always a fresh read; a partial page never warms an All cache.
type ResultPage[R any] struct {
	Total int64
	Rows  []R
}

func (q ResultQuery[M, R]) Page(ctx context.Context, limit, offset int) (ResultPage[R], error) {
	var zero ResultPage[R]
	if err := q.validate(ctx); err != nil {
		return zero, err
	}
	plan, err := query.NewPagePlan(q.plan, limit, offset)
	if err != nil {
		return zero, err
	}
	return readResultPage(ctx, q.origin.backend, plan, q.newDecoder, limit, offset)
}

func readResultPage[R any](ctx context.Context, backend db.Queryer, plan query.Plan, newDecoder func() resultDecoder[R], limit, offset int) (ResultPage[R], error) {
	var zero ResultPage[R]
	rows, err := openQueryRows(ctx, backend, plan)
	if err != nil {
		return zero, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	values := make([]resultDecoder[R], 0)
	var total int64
	seen, sentinel := false, false
	for rows.Next() {
		raw := make([]any, len(plan.ResultShape().Expressions())+2)
		targets := make([]any, len(raw))
		for index := range raw {
			targets[index] = &raw[index]
		}
		if err = rows.Scan(targets...); err != nil {
			break
		}
		var count, present sql.NullInt64
		if err = count.Scan(raw[0]); err != nil {
			break
		}
		if err = present.Scan(raw[1]); err != nil {
			break
		}
		if !count.Valid || count.Int64 < 0 || seen && count.Int64 != total || sentinel {
			err = invalidResultBuilder("result page returned inconsistent totals or presence")
			break
		}
		total, seen = count.Int64, true
		if !present.Valid {
			if len(values) != 0 {
				err = invalidResultBuilder("result page mixed rows and empty sentinel")
				break
			}
			for _, value := range raw[2:] {
				if value != nil {
					err = invalidResultBuilder("empty result page contains values")
					break
				}
			}
			if err != nil {
				break
			}
			sentinel = true
			continue
		}
		if present.Int64 != 1 || len(values) >= limit {
			err = invalidResultBuilder("result page exceeded its bound or returned invalid presence")
			break
		}
		decoder := newDecoder()
		if err = decoder.err; err != nil {
			break
		}
		if len(decoder.destinations) != len(raw)-2 {
			err = invalidResultBuilder("result page decoder does not match its selected columns")
			break
		}
		for index, destination := range decoder.destinations {
			if err = scanGroupValue(destination, raw[index+2]); err != nil {
				err = fmt.Errorf("scan result page cell %d: %w", index, err)
				break
			}
		}
		if err != nil {
			break
		}
		if err = decoder.validateScanned(); err != nil {
			break
		}
		values = append(values, decoder)
	}
	if err = lifecycle.finish(ctx, err); err != nil {
		return sessionReadResult(ctx, backend, zero, err)
	}
	if err := validateQuerySession(ctx, backend); err != nil {
		return zero, err
	}
	if !seen && !plan.EmptyResult() {
		return zero, invalidResultBuilder("result page returned no count row")
	}
	expected := max(int64(0), min(int64(limit), total-int64(offset)))
	if int64(len(values)) != expected {
		return zero, invalidResultBuilder("result page count and rows disagree")
	}
	result, err := publishResultCells(ctx, backend, values)
	if err != nil {
		return zero, err
	}
	return sessionReadResult(ctx, backend, ResultPage[R]{Total: total, Rows: result}, nil)
}
