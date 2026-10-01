package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// Keep the instrumentation on the supplied affine executor, not the original
// pool. This also tests wrapping a backend without losing origin identity.
type streamProbe struct {
	collectionBackend
	plans      []query.Plan
	failure    error
	failAt     int
	scanFailAt int
	scans      int
	closed     bool
	corrupt    func(int, query.Plan, db.Row) db.Row
}

func (p *streamProbe) read(ctx context.Context, backend db.Queryer, plan query.Plan) (db.Rows, error) {
	p.plans = append(p.plans, plan)
	if p.failAt == len(p.plans) {
		return nil, p.failure
	}
	return backend.Query(ctx, plan)
}
func (p *streamProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return p.read(ctx, p.collectionBackend, plan)
}
func (p *streamProbe) batches(ctx context.Context, backend db.BatchQueryer, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	p.plans = append(p.plans, plan)
	p.closed = false
	defer func() { p.closed = true }()
	return backend.QueryBatches(ctx, plan, size, func(row db.Row) error {
		p.scans++
		if p.scans == p.scanFailAt {
			return p.failure
		}
		if p.corrupt != nil {
			row = p.corrupt(p.scans, plan, row)
		}
		return scan(row)
	}, func(executor db.Queryer) (bool, error) {
		return yield(&streamExecutor{Session: executor.(db.Session), RelationAtomic: executor.(db.RelationAtomic), BatchQueryer: executor.(db.BatchQueryer), probe: p})
	})
}
func (p *streamProbe) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	return p.batches(ctx, p.collectionBackend.(db.BatchQueryer), plan, size, scan, yield)
}

type streamExecutor struct {
	db.Session
	db.RelationAtomic
	db.BatchQueryer
	probe *streamProbe
}

func (s *streamExecutor) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return s.probe.read(ctx, s.Session, plan)
}
func (s *streamExecutor) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	return s.probe.batches(ctx, s.BatchQueryer, plan, size, scan, yield)
}

type streamSession struct {
	db.Session
	db.SessionValidator
	db.BatchQueryer
	probe *streamProbe
}

func (s *streamSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return s.probe.read(ctx, s.Session, plan)
}
func (s *streamSession) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	s.probe.plans = append(s.probe.plans, plan)
	return s.BatchQueryer.QueryBatches(ctx, plan, size, scan, func(db.Queryer) (bool, error) { return yield(s) })
}

type streamCardinalityRow struct {
	db.Row
	plan  query.Plan
	owner int64
}

func (r streamCardinalityRow) Scan(destinations ...any) error {
	if err := r.Row.Scan(destinations...); err != nil {
		return err
	}
	for i, f := range r.plan.SourceFields() {
		if f.Name() == "id" {
			if err := destinations[i].(sql.Scanner).Scan(r.owner); err != nil {
				return err
			}
		}
	}
	offset := len(r.plan.SourceFields())
	for _, projection := range r.plan.RelationProjections() {
		for i, f := range projection.TargetColumns() {
			if f.Name() == "owner" {
				if err := destinations[offset+i].(sql.Scanner).Scan(r.owner); err != nil {
					return err
				}
			}
		}
		offset += len(projection.TargetColumns())
	}
	return nil
}

func TestMaterializedStream(t *testing.T) { withCollectionBackends(t, runMaterializedStream) }
func runMaterializedStream(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
	t.Cleanup(func() { check(t, b.Close()) })
	migrateCollections(t, b)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var rawOwners []owners.Owner
	var rawLabels []labels.Label
	for _, name := range []string{"a", "b", "c"} {
		v, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate(name))
		check(t, err)
		rawLabels = append(rawLabels, v)
	}
	collections, err := project.BindCollections()
	check(t, err)
	for i, indices := range [][]int{{0, 1}, {1}, {}, {2}} {
		v, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(fmt.Sprintf("owner-%d", i)))
		check(t, err)
		rawOwners = append(rawOwners, v)
		manager, err := collections.OwnersOwnerLabels.From(b, v)
		check(t, err)
		var targets []labels.Label
		for _, j := range indices {
			targets = append(targets, rawLabels[j])
		}
		check(t, manager.Add(ctx, targets))
		_, err = owners.RequiredOwnerObjects.Create(ctx, b, owners.RequiredOwnerCreate{}.WithOwnerID(v.ID).WithAmount(int64(i)))
		check(t, err)
		_, err = owners.BadgeObjects.Create(ctx, b, owners.BadgeCreate{}.WithOwnerID(v.ID).WithName(fmt.Sprintf("badge-%d", i)))
		check(t, err)
	}
	var document struct {
		Observations map[string]json.RawMessage `json:"observations"`
	}
	data, err := os.ReadFile("django-query.json")
	check(t, err)
	check(t, json.Unmarshal(data, &document))
	var reference map[string]json.RawMessage
	check(t, json.Unmarshal(document.Observations["prefetch_stream"], &reference))
	relations, err := project.BindRelations()
	check(t, err)
	for _, name := range []string{"chunk_1", "chunk_2", "chunk_3", "chunk_8", "invalid_zero", "invalid_negative", "early_stop", "callback_error", "nested_filtered", "owner_scope_1", "owner_scope_2", "warm_cache", "transaction"} {
		t.Run(name, func(t *testing.T) {
			probe := &streamProbe{collectionBackend: b}
			api, err := project.Using(probe)
			check(t, err)
			size := 2
			switch name {
			case "chunk_1", "owner_scope_1":
				size = 1
			case "chunk_3":
				size = 3
			case "chunk_8":
				size = 8
			case "invalid_zero":
				size = 0
			case "invalid_negative":
				size = -1
			}
			execute := func(api project.Models) {
				base := api.OwnersOwner.OrderBy(owners.OwnerFields.Name.Asc())
				selection := base.Prefetch.Labels
				if name == "nested_filtered" {
					selection = selection.Filter(labels.LabelFields.Name.In("a", "b")).OrderBy(labels.LabelFields.Name.Desc()).WithChildren(api.LabelsLabel.Prefetch.Owners)
				}
				snapshot := strings.HasPrefix(name, "owner_scope")
				if snapshot {
					selection, err = selection.Snapshot("label_rows").Filter(relations.LabelsLabel.Owners.Name.Exact("owner-0")).Filter(relations.LabelsLabel.Owners.Name.Exact("owner-1")).OrderBy(labels.LabelFields.Name.Asc()).Limit(1)
					check(t, err)
				}
				q := base.PrefetchRelated(selection)
				graph := func(callCtx context.Context, row *project.OwnersOwner) any {
					var children []*project.LabelsLabel
					if snapshot {
						var present bool
						children, present, err = selection.Read(callCtx, row)
						check(t, err)
						if !present {
							t.Fatal("snapshot missing")
						}
					} else {
						view, e := row.Labels()
						check(t, e)
						children, err = view.All(callCtx)
						check(t, err)
					}
					values := make([]any, 0, len(children))
					for _, child := range children {
						if name != "nested_filtered" {
							values = append(values, child.Name)
							continue
						}
						reverse, e := child.Owners()
						check(t, e)
						parents, e := reverse.All(callCtx)
						check(t, e)
						names := make([]string, len(parents))
						for i, p := range parents {
							names[i] = p.Name
						}
						values = append(values, []any{child.Name, names})
					}
					return []any{row.Name, values}
				}
				graphs := func(callCtx context.Context, rows []*project.OwnersOwner) []any {
					result := make([]any, 0, len(rows))
					for _, row := range rows {
						result = append(result, graph(callCtx, row))
					}
					return result
				}
				result := map[string]any{}
				if name == "warm_cache" {
					held, e := q.All(ctx)
					check(t, e)
					result["held"] = graphs(ctx, held)
					value := rawLabels[0]
					value.Name = "a-new"
					check(t, labels.LabelObjects.Save(ctx, b, &value))
					defer func() { value.Name = "a"; check(t, labels.LabelObjects.Save(ctx, b, &value)) }()
				}
				members := []any{}
				before := len(probe.plans)
				boom := errors.New("callback failed")
				streamErr := q.Iterate(ctx, size, func(callCtx context.Context, row *project.OwnersOwner) (bool, error) {
					members = append(members, graph(callCtx, row))
					if name == "callback_error" {
						return false, boom
					}
					return name != "early_stop", nil
				})
				var errorName any
				switch {
				case strings.HasPrefix(name, "invalid_"):
					if !errors.Is(streamErr, &query.Error{Code: query.CodeInvalidValue}) {
						t.Fatal(streamErr)
					}
					errorName = "ValueError"
				case name == "callback_error":
					if !errors.Is(streamErr, boom) {
						t.Fatal(streamErr)
					}
					errorName = "RuntimeError"
				default:
					check(t, streamErr)
				}
				result["members"] = members
				result["queries"] = len(probe.plans) - before
				result["error"] = errorName
				before = len(probe.plans)
				full, e := q.All(ctx)
				check(t, e)
				result["full_after"] = graphs(ctx, full)
				result["full_after_queries"] = len(probe.plans) - before
				if name == "transaction" {
					result["count_after_close"] = 4
				}
				prefetchReference(t, reference, name, result)
			}
			if name != "transaction" {
				execute(api)
				return
			}
			var held *project.OwnersOwner
			check(t, b.(db.Atomic).Atomic(ctx, func(session db.Session) error {
				s := &streamSession{Session: session, SessionValidator: session.(db.SessionValidator), BatchQueryer: session.(db.BatchQueryer), probe: probe}
				scoped, e := project.UsingSession(s)
				check(t, e)
				execute(scoped)
				return scoped.OwnersOwner.Iterate(ctx, 2, func(callCtx context.Context, row *project.OwnersOwner) (bool, error) {
					held = row
					view, e := row.Labels()
					check(t, e)
					if e = view.Add(callCtx, nil); e == nil {
						t.Fatal("ordinary session gained relation mutation capability")
					}
					return false, nil
				})
			}))
			if _, e := held.Labels(); e == nil {
				t.Fatal("borrowed model survived session")
			}
			count, e := api.OwnersOwner.Count(ctx)
			check(t, e)
			if count != 4 {
				t.Fatal(count)
			}
		})
	}
	t.Run("affinity_origin_writes", func(t *testing.T) {
		probe := &streamProbe{collectionBackend: b}
		api, e := project.Using(probe)
		check(t, e)
		original, _, e := api.OwnersOwner.OrderBy(owners.OwnerFields.Name.Asc()).First(ctx)
		check(t, e)
		foreign, e := project.Using(probe)
		check(t, e)
		foreignOwner, _, e := foreign.OwnersOwner.OrderBy(owners.OwnerFields.Name.Asc()).First(ctx)
		check(t, e)
		view, e := original.Labels()
		check(t, e)
		var held *project.OwnersRequiredOwner
		check(t, api.OwnersRequiredOwner.OrderBy(owners.RequiredOwnerFields.Amount.Asc()).Iterate(ctx, 2, func(callCtx context.Context, row *project.OwnersRequiredOwner) (bool, error) {
			held, e = row.WithOwner(original)
			check(t, e)
			same, e := held.Owner(callCtx)
			check(t, e)
			if same != original {
				t.Fatal("assigned model identity changed")
			}
			if _, e = row.WithOwner(foreignOwner); e == nil {
				t.Fatal("foreign facade origin accepted")
			}
			check(t, held.Save(callCtx))
			check(t, original.Save(callCtx))
			fresh, _, e := api.OwnersOwner.Filter(owners.OwnerFields.Name.Exact(original.Name)).OrderBy(owners.OwnerFields.Name.Asc()).First(callCtx)
			check(t, e)
			if fresh.ID != original.ID {
				t.Fatal(fresh)
			}
			// This manager existed before the stream and must still route writes to its source connection.
			targets, e := view.All(callCtx)
			check(t, e)
			check(t, view.Set(callCtx, targets))
			made, e := owners.OwnerObjects.Create(callCtx, probe, owners.NewOwnerCreate("temporary"))
			check(t, e)
			made, e = owners.OwnerObjects.Update(callCtx, probe, made, owners.OwnerPatch{}.WithName("temporary-updated"))
			check(t, e)
			_, e = owners.OwnerObjects.Delete(callCtx, probe, &made)
			check(t, e)
			made, e = owners.OwnerObjects.Create(callCtx, probe, owners.NewOwnerCreate("atomic-delete"))
			check(t, e)
			deleters, e := project.BindRelationDeleters()
			check(t, e)
			deleted, e := deleters.OwnersOwner.Delete(callCtx, probe, &made)
			check(t, e)
			if deleted != 1 {
				t.Fatal(deleted)
			}
			return false, nil
		}))
		check(t, held.Save(ctx))
		if !probe.closed {
			t.Fatal("source not closed")
		}
	})
	t.Run("eager_reverse_and_source_slice", func(t *testing.T) {
		api, e := project.Using(b)
		check(t, e)
		base := api.OwnersRequiredOwner.OrderBy(owners.RequiredOwnerFields.Amount.Asc())
		limited, e := base.Offset(1)
		check(t, e)
		limited, e = limited.Limit(2)
		check(t, e)
		q := limited.SelectRelated(limited.Related.Owner)
		var got []int64
		check(t, q.Iterate(ctx, 1, func(callCtx context.Context, row *project.OwnersRequiredOwner) (bool, error) {
			owner, e := row.Owner(callCtx)
			check(t, e)
			if owner.Name != fmt.Sprintf("owner-%d", row.Amount) {
				t.Fatal(owner.Name, row.Amount)
			}
			got = append(got, row.Amount)
			return true, nil
		}))
		if !reflect.DeepEqual(got, []int64{1, 2}) {
			t.Fatal(got)
		}
		reverse := api.OwnersOwner.OrderBy(owners.OwnerFields.Name.Asc()).SelectRelated(api.OwnersOwner.Related.Badge).PrefetchRelated(api.OwnersOwner.Prefetch.Badge.WithChildren(api.OwnersBadge.Prefetch.Owner.WithChildren(api.OwnersOwner.Prefetch.Labels)))
		count := 0
		check(t, reverse.Iterate(ctx, 2, func(callCtx context.Context, row *project.OwnersOwner) (bool, error) {
			badge, present, e := row.Badge(callCtx)
			check(t, e)
			if !present {
				t.Fatal("badge missing")
			}
			owner, e := badge.Owner(callCtx)
			check(t, e)
			view, e := owner.Labels()
			check(t, e)
			_, e = view.All(callCtx)
			check(t, e)
			count++
			return true, nil
		}))
		if count != 4 {
			t.Fatal(count)
		}
	})
	t.Run("paths_duplicates_and_cache", func(t *testing.T) {
		api, e := project.Using(b)
		check(t, e)
		q, e := api.OwnersOwner.Filter(relations.OwnersOwner.Labels.Name.In("a", "b")).OrderBy(owners.OwnerFields.Name.Asc()).PrefetchRelatedPaths("labels__owners")
		check(t, e)
		var rows []*project.OwnersOwner
		check(t, q.Iterate(ctx, 2, func(callCtx context.Context, row *project.OwnersOwner) (bool, error) {
			rows = append(rows, row)
			view, e := row.Labels()
			check(t, e)
			targets, e := view.All(callCtx)
			check(t, e)
			for _, target := range targets {
				view, e := target.Owners()
				check(t, e)
				_, e = view.All(callCtx)
				check(t, e)
			}
			return true, nil
		}))
		if len(rows) != 3 || rows[0] == rows[1] || rows[0].ID != rows[1].ID {
			t.Fatal(rows)
		}
		rows[0].Name = "private"
		if rows[1].Name != "owner-0" {
			t.Fatal("duplicate models alias")
		}
		full, e := q.All(ctx)
		check(t, e)
		if len(full) != 3 || full[0].Name != "owner-0" {
			t.Fatal(full)
		}
	})
	t.Run("session_variants", func(t *testing.T) {
		for _, name := range []string{"relation", "coordinated", "coordinated_relation"} {
			t.Run(name, func(t *testing.T) {
				rollback := errors.New("outer rollback")
				var held *project.OwnersOwner
				calls := 0
				callback := func(session db.Session) error {
					api, e := project.UsingSession(session)
					if e != nil {
						return e
					}
					e = api.OwnersOwner.OrderBy(owners.OwnerFields.Name.Asc()).PrefetchRelated(api.OwnersOwner.Prefetch.Labels).Iterate(ctx, 2, func(callCtx context.Context, row *project.OwnersOwner) (bool, error) {
						calls++
						held = row
						view, e := row.Labels()
						if e != nil {
							return false, e
						}
						targets, e := view.All(callCtx)
						if e != nil {
							return false, e
						}
						if _, relation := session.(db.RelationSession); !relation {
							if e = view.Add(callCtx, nil); e == nil {
								return false, errors.New("read-only session gained relation capability")
							}
						} else if e = view.Set(callCtx, targets); e != nil {
							return false, e
						}
						row.Name = "provisional"
						return true, row.Save(callCtx)
					})
					if e != nil {
						return e
					}
					return rollback
				}
				var e error
				switch name {
				case "relation":
					e = b.AtomicRelation(ctx, func(s db.RelationSession) error { return callback(s) })
				case "coordinated":
					e = b.(db.CoordinatedAtomic).CoordinatedAtomic(ctx, callback)
				case "coordinated_relation":
					e = b.CoordinatedAtomicRelation(ctx, func(s db.RelationSession) error { return callback(s) })
				}
				if !errors.Is(e, rollback) || calls != 4 {
					t.Fatal(e, calls)
				}
				if _, e := held.Labels(); e == nil {
					t.Fatal("expired session model accepted")
				}
				api, e := project.Using(b)
				check(t, e)
				n, e := api.OwnersOwner.Filter(owners.OwnerFields.Name.Exact("provisional")).Count(ctx)
				check(t, e)
				if n != 0 {
					t.Fatal("outer rollback failed", n)
				}
			})
		}
	})

	t.Run("failure_cancel_panic", func(t *testing.T) {
		for _, mode := range []string{"child_first", "child_second", "source_second", "cancel", "panic"} {
			t.Run(mode, func(t *testing.T) {
				boom := errors.New("injected stream failure")
				probe := &streamProbe{collectionBackend: b, failure: boom}
				api, e := project.Using(probe)
				check(t, e)
				q := api.OwnersOwner.OrderBy(owners.OwnerFields.Name.Asc()).PrefetchRelated(api.OwnersOwner.Prefetch.Labels)
				if mode == "child_first" {
					probe.failAt = 2
				}
				if mode == "child_second" {
					probe.failAt = 3
				}
				if mode == "source_second" {
					probe.scanFailAt = 3
				}
				callCtx, stop := context.WithCancel(ctx)
				defer stop()
				calls := 0
				func() {
					defer func() {
						if mode == "panic" {
							if recover() != boom {
								t.Fatal("panic identity lost")
							}
						}
					}()
					e = q.Iterate(callCtx, 2, func(context.Context, *project.OwnersOwner) (bool, error) {
						calls++
						if mode == "panic" {
							panic(boom)
						}
						if mode == "cancel" {
							stop()
						}
						return true, nil
					})
					if mode == "panic" {
						t.Fatal("panic swallowed")
					}
				}()
				want := 2
				if mode == "child_first" {
					want = 0
				}
				if mode == "panic" || mode == "cancel" {
					want = 1
				}
				if calls != want || !probe.closed {
					t.Fatal(mode, calls, probe.closed, e)
				}
				if mode == "cancel" {
					if !errors.Is(e, context.Canceled) {
						t.Fatal(e)
					}
				} else if mode != "panic" && !errors.Is(e, boom) {
					t.Fatal(e)
				}
				probe.failAt = 0
				probe.scanFailAt = 0
				full, e := q.All(ctx)
				check(t, e)
				if len(full) != 4 {
					t.Fatal(len(full))
				}
			})
		}
	})
	t.Run("cross_batch_cardinality", func(t *testing.T) {
		probe := &streamProbe{collectionBackend: b, corrupt: func(n int, plan query.Plan, row db.Row) db.Row {
			if n == 2 {
				return streamCardinalityRow{Row: row, plan: plan, owner: rawOwners[0].ID}
			}
			return row
		}}
		api, e := project.Using(probe)
		check(t, e)
		q := api.OwnersOwner.OrderBy(owners.OwnerFields.Name.Asc()).SelectRelated(api.OwnersOwner.Related.Badge)
		calls := 0
		e = q.Iterate(ctx, 1, func(context.Context, *project.OwnersOwner) (bool, error) { calls++; return true, nil })
		if calls != 1 || !errors.Is(e, &query.Error{Code: query.CodeRelatedObjectCardinality}) || !probe.closed {
			t.Fatal(calls, e, probe.closed)
		}
	})

	t.Run("nested_stream", func(t *testing.T) {
		api, e := project.Using(b)
		check(t, e)
		outer, inner := 0, 0
		check(t, api.OwnersOwner.Iterate(ctx, 2, func(callCtx context.Context, row *project.OwnersOwner) (bool, error) {
			outer++
			return true, api.LabelsLabel.Iterate(callCtx, 1, func(nested context.Context, label *project.LabelsLabel) (bool, error) {
				inner++
				return true, label.Save(nested)
			})
		}))
		if outer != 4 || inner != 12 {
			t.Fatal(outer, inner)
		}
	})
	t.Run("concurrent_contexts", func(t *testing.T) {
		api, e := project.Using(b)
		check(t, e)
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			wg.Go(func() {
				count := 0
				e := api.OwnersOwner.PrefetchRelated(api.OwnersOwner.Prefetch.Labels).Iterate(ctx, 2, func(callCtx context.Context, row *project.OwnersOwner) (bool, error) {
					count++
					view, e := row.Labels()
					if e != nil {
						return false, e
					}
					_, e = view.All(callCtx)
					return true, e
				})
				if e == nil && count != 4 {
					e = fmt.Errorf("count %d", count)
				}
				results <- e
			})
		}
		wg.Wait()
		close(results)
		for e := range results {
			check(t, e)
		}
	})
	t.Run("unsupported_and_empty", func(t *testing.T) {
		// Embedding only the base interface intentionally hides the batch capability.
		hidden := struct{ collectionBackend }{b}
		api, e := project.Using(hidden)
		check(t, e)
		q := api.OwnersOwner.Filter(owners.OwnerFields.Name.Exact("absent"))
		if e = q.Iterate(ctx, 2, func(context.Context, *project.OwnersOwner) (bool, error) {
			t.Fatal("unexpected callback")
			return true, nil
		}); !errors.Is(e, &query.Error{Code: query.CodeUnsupported}) {
			t.Fatal(e)
		}
		api, e = project.Using(b)
		check(t, e)
		q = api.OwnersOwner.Filter(owners.OwnerFields.Name.Exact("absent"))
		check(t, q.Iterate(ctx, 2, func(context.Context, *project.OwnersOwner) (bool, error) {
			t.Fatal("unexpected callback")
			return true, nil
		}))
		if e = q.Iterate(ctx, 2, nil); !errors.Is(e, &query.Error{Code: query.CodeInvalidValue}) {
			t.Fatal(e)
		}
	})
}
