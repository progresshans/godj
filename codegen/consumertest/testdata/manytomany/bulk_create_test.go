package consumer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

type bulkProbe struct {
	collectionBackend
	plans                    []query.BulkInsertPlan
	transactions, savepoints int
	limits                   db.BulkInsertLimits
	before, after            func(int) error
	afterCommit              func()
}

func (probe *bulkProbe) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	return probe.collectionBackend.(db.BulkInserter).BulkInsertLimits(ctx)
}
func (probe *bulkProbe) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	return probe.collectionBackend.(db.BulkInserter).BulkInsert(ctx, plan)
}
func (probe *bulkProbe) Atomic(ctx context.Context, callback func(db.Session) error) error {
	probe.transactions++
	err := probe.collectionBackend.(db.Atomic).Atomic(ctx, func(session db.Session) error { return callback(bulkObservedSession{Session: session, probe: probe}) })
	if err == nil && probe.afterCommit != nil {
		probe.afterCommit()
	}
	return err
}

type bulkObservedSession struct {
	db.Session
	probe *bulkProbe
}

func (session bulkObservedSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session bulkObservedSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	session.probe.savepoints++
	return db.WithSavepoint(ctx, session.Session, func(child db.Session) error {
		return callback(bulkObservedSession{Session: child, probe: session.probe})
	})
}
func (session bulkObservedSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	limits, err := session.Session.(db.BulkInserter).BulkInsertLimits(ctx)
	if session.probe.limits.Rows > 0 {
		limits.Rows = min(limits.Rows, session.probe.limits.Rows)
	}
	if session.probe.limits.Parameters > 0 {
		limits.Parameters = min(limits.Parameters, session.probe.limits.Parameters)
	}
	return limits, err
}
func (session bulkObservedSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	index := len(session.probe.plans)
	session.probe.plans = append(session.probe.plans, plan)
	if session.probe.before != nil {
		if err := session.probe.before(index); err != nil {
			return db.BulkInsertResult{}, err
		}
	}
	result, err := session.Session.(db.BulkInserter).BulkInsert(ctx, plan)
	if err == nil && session.probe.after != nil {
		err = session.probe.after(index)
	}
	return result, err
}

func TestBulkCreate(t *testing.T) { withCollectionBackends(t, runBulkCreate) }

func runBulkCreate(t *testing.T, backend collectionBackend, open func() (collectionBackend, error), _ func(string) error, postgres bool) {
	t.Cleanup(func() { check(t, backend.Close()) })
	migrateCollections(t, backend)
	ctx := t.Context()
	api, err := project.Using(backend)
	check(t, err)
	owner, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("bulk relation owner"))
	check(t, err)

	t.Run("input_order_keys_defaults_and_cache", func(t *testing.T) {
		probe := &bulkProbe{collectionBackend: backend, limits: db.BulkInsertLimits{Rows: 3, Parameters: 6}}
		facade, err := project.Using(probe)
		check(t, err)
		source := facade.LabelsLabel.Filter(labels.LabelFields.Name.Exact("read predicate is ignored"))
		warm, err := source.All(ctx)
		check(t, err)
		values := []labels.Label{{Name: "bulk-auto-first", Note: new("shared")}, labels.NewLabelWithID(9007199254740993), labels.NewLabelWithID(0), {Name: "bulk-auto-last"}, labels.NewLabelWithID(-9)}
		values[1].Name, values[2].Name, values[4].Name = "bulk-large", "bulk-zero", "bulk-negative"
		original := slices.Clone(values)
		result, err := source.BulkCreate(ctx, values, orm.BulkBatchSize[labels.Label](2))
		check(t, err)
		if !result.ReturnedKeys || result.RowsAffected != 5 || len(result.Objects) != 5 || len(probe.plans) != 3 || probe.transactions != 1 || !reflect.DeepEqual(values, original) {
			t.Fatal("bulk order/batch/input ownership", result, len(probe.plans))
		}
		for index, value := range result.Objects {
			stored, err := api.LabelsLabel.Filter(labels.LabelFields.ID.Exact(value.ID)).Get(ctx)
			check(t, err)
			if value.Name != values[index].Name || stored.Name != value.Name {
				t.Fatal("RETURNING keys not paired with input", index, value, stored)
			}
			if _, present := (labels.LabelDescriptor{}).PrimaryKey(values[index]); present && value.ID != values[index].ID {
				t.Fatal("explicit key changed")
			}
		}
		*result.Objects[0].Note = "private result"
		if *values[0].Note != "shared" {
			t.Fatal("returned nullable data aliases input")
		}
		cached, err := source.All(ctx)
		check(t, err)
		if !reflect.DeepEqual(cached, warm) {
			t.Fatal("bulk changed warm read cache")
		}
		created, err := facade.LabelsLabel.BulkCreateInputs(ctx, []labels.LabelCreate{labels.NewLabelCreate("builder default"), labels.NewLabelCreate("builder explicit null").WithNoteNull()})
		check(t, err)
		if len(created.Objects) != 2 || created.Objects[0].Note != nil || created.Objects[1].Note != nil {
			t.Fatal("builder null/default semantics", created)
		}
	})

	t.Run("all_input_prepared_before_insert", func(t *testing.T) {
		probe := &bulkProbe{collectionBackend: backend}
		builds := 0
		input := singleCreate[labels.Label](func() orm.Mutation[labels.Label] {
			builds++
			if len(probe.plans) != 0 {
				t.Fatal("partial write before preparing all inputs")
			}
			return labels.LabelCreate{}.BuildCreate()
		})
		result, err := labels.LabelObjects.BulkCreateInputs(ctx, probe, []orm.CreateInput[labels.Label]{labels.NewLabelCreate("prepare must roll back"), input}, orm.BulkBatchSize[labels.Label](1))
		if err == nil || result.Objects != nil || builds != 1 || len(probe.plans) != 0 {
			t.Fatal("invalid final builder wrote earlier row", result, err)
		}
		count, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact("prepare must roll back")).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("input failure stored data")
		}
	})

	t.Run("all_batches_rollback", func(t *testing.T) {
		_, err := api.LabelsLabel.BulkCreateInputs(ctx, []labels.LabelCreate{labels.NewLabelCreate("existing unique")})
		check(t, err)
		probe := &bulkProbe{collectionBackend: backend}
		result, err := labels.LabelObjects.BulkCreateInputs(ctx, probe, orm.CreateInputs[labels.Label]([]labels.LabelCreate{labels.NewLabelCreate("rollback first batch"), labels.NewLabelCreate("existing unique")}), orm.BulkBatchSize[labels.Label](1))
		if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) || result.Objects != nil || result.RowsAffected != 0 || len(probe.plans) != 2 {
			t.Fatal("late unique failure lost classification or leaked output", result, err)
		}
		count, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact("rollback first batch")).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("early batch escaped atomic rollback")
		}
	})

	t.Run("ignore_unique_only", func(t *testing.T) {
		result, err := api.LabelsLabel.BulkCreateInputs(ctx, []labels.LabelCreate{labels.NewLabelCreate("existing unique"), labels.NewLabelCreate("ignored new"), labels.NewLabelCreate("existing unique")}, orm.BulkIgnoreConflicts[labels.Label]())
		check(t, err)
		if result.ReturnedKeys || result.RowsAffected != 1 || len(result.Objects) != 3 {
			t.Fatal("ignore guessed per-input disposition", result)
		}
		for _, object := range result.Objects {
			raw, err := object.Unwrap()
			check(t, err)
			if _, present := (labels.LabelDescriptor{}).PrimaryKey(raw); present {
				t.Fatal("ignore assigned an ambiguous generated key")
			}
		}
		failed, err := api.OwnersRequiredOwner.BulkCreateInputs(ctx, []owners.RequiredOwnerCreate{owners.NewRequiredOwnerCreate(owner.ID, 500), owners.NewRequiredOwnerCreate(-999, 501)}, orm.BulkIgnoreConflicts[owners.RequiredOwner](), orm.BulkBatchSize[owners.RequiredOwner](1))
		if err == nil || failed.Objects != nil {
			t.Fatal("ignore suppressed foreign-key integrity", failed, err)
		}
		count, err := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.Amount.Exact(500)).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("foreign key failure preserved an earlier batch")
		}
	})

	t.Run("update_target_and_native_duplicates", func(t *testing.T) {
		original, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact("existing unique")).Get(ctx)
		check(t, err)
		policy := orm.BulkUpdateConflicts([]orm.BulkConflictField[labels.Label]{labels.LabelFields.Name}, labels.LabelFields.Note)
		result, err := api.LabelsLabel.BulkCreateInputs(ctx, []labels.LabelCreate{labels.NewLabelCreate("existing unique").WithNote("changed"), labels.NewLabelCreate("upsert created").WithNote("new")}, policy)
		check(t, err)
		if !result.ReturnedKeys || result.RowsAffected != 2 || result.Objects[0].ID != original.ID {
			t.Fatal("upsert did not return existing ID", result)
		}
		stored, err := api.LabelsLabel.Filter(labels.LabelFields.ID.Exact(original.ID)).Get(ctx)
		check(t, err)
		if stored.Note == nil || *stored.Note != "changed" {
			t.Fatal("upsert lost selected update", stored)
		}
		for _, size := range []int{2, 1} {
			name := fmt.Sprint("duplicate bulk update ", size)
			input := []labels.LabelCreate{labels.NewLabelCreate(name).WithNote("first"), labels.NewLabelCreate(name).WithNote("last")}
			result, err := api.LabelsLabel.BulkCreateInputs(ctx, input, policy, orm.BulkBatchSize[labels.Label](size))
			if postgres && size == 2 {
				if err == nil || result.Objects != nil {
					t.Fatal("native PostgreSQL duplicate upsert was hidden", result, err)
				}
				count, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact(name)).Count(ctx)
				check(t, err)
				if count != 0 {
					t.Fatal("failed duplicate update left rows")
				}
			} else {
				check(t, err)
				if len(result.Objects) != 2 || result.Objects[0].ID != result.Objects[1].ID || *result.Objects[0].Note != "first" || *result.Objects[1].Note != "last" {
					t.Fatal("native duplicate update/input snapshots", result)
				}
				stored, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact(name)).Get(ctx)
				check(t, err)
				if *stored.Note != "last" {
					t.Fatal("last native update lost", stored)
				}
			}
		}
		input := labels.NewLabelWithID(original.ID)
		input.Name = "input name is not in update mask"
		input.Note = new("primary target update")
		result, err = api.LabelsLabel.BulkCreate(ctx, []labels.Label{input}, orm.BulkUpdateConflictNames[labels.Label]([]string{"id"}, []string{"note"}))
		check(t, err)
		stored, err = api.LabelsLabel.Filter(labels.LabelFields.ID.Exact(original.ID)).Get(ctx)
		check(t, err)
		if stored.Name != original.Name || result.Objects[0].Name != input.Name || *stored.Note != *input.Note {
			t.Fatal("upsert refreshed input or updated an omitted field")
		}
	})

	t.Run("composite_target_and_relations", func(t *testing.T) {
		label, err := labels.LabelObjects.Create(ctx, backend, labels.NewLabelCreate("composite label"))
		check(t, err)
		first, err := api.OwnersRankedLink.BulkCreateInputs(ctx, []owners.RankedLinkCreate{owners.NewRankedLinkCreate(901).WithOwnerID(owner.ID).WithLabelID(label.ID)})
		check(t, err)
		changed, err := api.OwnersRankedLink.BulkCreateInputs(ctx, []owners.RankedLinkCreate{owners.NewRankedLinkCreate(902).WithOwnerID(owner.ID).WithLabelID(label.ID)}, orm.BulkUpdateConflictNames[owners.RankedLink]([]string{"label", "owner"}, []string{"amount"}))
		check(t, err)
		if first.Objects[0].ID != changed.Objects[0].ID || changed.Objects[0].Amount != 902 {
			t.Fatal("composite key conflict failed", first, changed)
		}
		related, found, err := changed.Objects[0].Owner(ctx)
		check(t, err)
		if !found || related == nil || related.ID != owner.ID {
			t.Fatal("returned relation points to expired bulk scope")
		}
		query := api.OwnersRequiredOwner
		created, err := query.BulkCreateInputs(ctx, []owners.RequiredOwnerCreate{owners.NewRequiredOwnerCreate(owner.ID, 1000), owners.NewRequiredOwnerCreate(owner.ID, 1001)})
		check(t, err)
		for _, object := range created.Objects {
			parent, err := object.Owner(ctx)
			check(t, err)
			if parent.ID != owner.ID {
				t.Fatal("bulk relation identity")
			}
			object.Amount++
			check(t, object.Save(ctx))
		}
	})

	t.Run("borrowed_scopes", func(t *testing.T) {
		for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
			t.Run(mode, func(t *testing.T) {
				for _, finish := range []string{"commit", "rollback"} {
					t.Run(finish, func(t *testing.T) {
						name := "bulk borrowed " + mode + " " + finish
						rollback := errors.New("rollback bulk parent")
						var held *project.OwnersRequiredOwner
						var scopedQuery project.LabelsLabelQuery
						err := runSingleScope(ctx, backend, mode, func(session db.Session) error {
							scoped, err := project.UsingSession(session)
							if err != nil {
								return err
							}
							scopedQuery = scoped.LabelsLabel
							_, err = labels.LabelObjects.Create(ctx, session, labels.NewLabelCreate("before "+name))
							if err != nil {
								return err
							}
							result, err := scoped.OwnersRequiredOwner.BulkCreateInputs(ctx, []owners.RequiredOwnerCreate{owners.NewRequiredOwnerCreate(owner.ID, 777)})
							if err != nil {
								return err
							}
							held = result.Objects[0]
							parent, err := held.Owner(ctx)
							if err != nil {
								return err
							}
							if parent.ID != owner.ID {
								return errors.New("bulk handle escaped borrowed parent")
							}
							// The failed child may roll back without poisoning a usable parent.
							failed, err := scoped.LabelsLabel.BulkCreateInputs(ctx, []labels.LabelCreate{labels.NewLabelCreate("failed " + name), labels.NewLabelCreate("existing unique")}, orm.BulkBatchSize[labels.Label](1))
							if !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) || failed.Objects != nil {
								return fmt.Errorf("failed child lost unique error: %w", err)
							}
							_, err = labels.LabelObjects.Create(ctx, session, labels.NewLabelCreate("after "+name))
							if err != nil {
								return err
							}
							if finish == "rollback" {
								return rollback
							}
							return nil
						})
						if finish == "rollback" {
							if !errors.Is(err, rollback) {
								t.Fatal(err)
							}
						} else {
							check(t, err)
						}
						for _, prefix := range []string{"before ", "after ", "failed "} {
							count, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact(prefix + name)).Count(ctx)
							check(t, err)
							want := int64(0)
							if finish == "commit" && prefix != "failed " {
								want = 1
							}
							if count != want {
								t.Fatal("parent/child commit boundary", prefix, count, want)
							}
						}
						count, err := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(held.ID)).Count(ctx)
						check(t, err)
						if (count == 1) != (finish == "commit") {
							t.Fatal("released bulk rows escaped parent outcome", count)
						}
						if _, err := held.Owner(ctx); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
							t.Fatal("expired cached relation remained usable", err)
						}
						if _, err := scopedQuery.BulkCreate(ctx, nil); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
							t.Fatal("expired empty bulk accepted", err)
						}
					})
				}
			})
		}
	})

	t.Run("root_batch_affinity", func(t *testing.T) {
		calls := 0
		err := api.OwnersOwner.Filter(owners.OwnerFields.ID.Exact(owner.ID)).Iterate(ctx, 1, func(batchContext context.Context, value *project.OwnersOwner) (bool, error) {
			calls++
			result, err := labels.LabelObjects.BulkCreateInputs(batchContext, backend, orm.CreateInputs[labels.Label]([]labels.LabelCreate{labels.NewLabelCreate("bulk inside root cursor"), labels.NewLabelCreate("bulk inside root cursor second")}))
			if err != nil {
				return false, err
			}
			if result.RowsAffected != 2 || !result.ReturnedKeys || value.ID != owner.ID {
				return false, errors.New("bulk root cursor result")
			}
			return false, nil
		})
		check(t, err)
		if calls != 1 {
			t.Fatal("missing root cursor callback", calls)
		}
		count, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact("bulk inside root cursor")).Count(ctx)
		check(t, err)
		if count != 1 {
			t.Fatal("root cursor bulk write lost")
		}
	})

	t.Run("readonly_and_cancellation", func(t *testing.T) {
		check(t, backend.(db.SnapshotReader).ReadSnapshot(ctx, func(reader db.Queryer) error {
			if _, ok := reader.(db.BulkInserter); ok {
				return errors.New("read-only snapshot advertises bulk capability")
			}
			result, err := labels.LabelObjects.BulkCreate(ctx, reader, nil)
			if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || result.Objects != nil {
				return fmt.Errorf("read-only snapshot accepted empty bulk: %w", err)
			}
			return nil
		}))
		callCtx, cancel := context.WithCancel(ctx)
		probe := &bulkProbe{collectionBackend: backend, after: func(int) error { cancel(); return nil }}
		result, err := labels.LabelObjects.BulkCreateInputs(callCtx, probe, orm.CreateInputs[labels.Label]([]labels.LabelCreate{labels.NewLabelCreate("canceled bulk first"), labels.NewLabelCreate("canceled bulk second")}), orm.BulkBatchSize[labels.Label](1))
		cancel()
		if !errors.Is(err, context.Canceled) || result.Objects != nil || len(probe.plans) != 1 {
			t.Fatal("canceled bulk exposed data or started next batch", result, err)
		}
		count, err := api.LabelsLabel.Filter(labels.LabelFields.Name.Exact("canceled bulk first")).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("cancellation failed to roll back first batch")
		}
		callCtx, cancel = context.WithCancel(ctx)
		probe = &bulkProbe{collectionBackend: backend, afterCommit: cancel}
		bound, err := project.Using(probe)
		check(t, err)
		committed, err := bound.LabelsLabel.BulkCreateInputs(callCtx, []labels.LabelCreate{labels.NewLabelCreate("committed before cancellation")})
		cancel()
		check(t, err)
		if committed.RowsAffected != 1 || len(committed.Objects) != 1 || callCtx.Err() == nil {
			t.Fatal("late cancellation changed confirmed bulk outcome", committed)
		}
	})

	t.Run("auto_key_only", func(t *testing.T) {
		result, err := api.OwnersBulkOnly.BulkCreateInputs(ctx, []owners.BulkOnlyCreate{{}, {}, {}}, orm.BulkBatchSize[owners.BulkOnly](2))
		check(t, err)
		if !result.ReturnedKeys || result.RowsAffected != 3 || len(result.Objects) != 3 {
			t.Fatal("auto-only result", result)
		}
		for _, object := range result.Objects {
			_, err := api.OwnersBulkOnly.Filter(owners.BulkOnlyFields.ID.Exact(object.ID)).Get(ctx)
			check(t, err)
		}
		count, err := api.OwnersBulkOnly.Count(ctx)
		check(t, err)
		if count != 3 {
			t.Fatal("auto-only rows", count)
		}
	})

	t.Run("scalar_codecs", func(t *testing.T) {
		cost, err := decimal.Parse("-12345.67")
		check(t, err)
		at, err := clock.Parse("12:34:56.789123")
		check(t, err)
		day, err := calendar.Parse("2024-02-29")
		check(t, err)
		reference, err := uuid.Parse("12345678-9abc-4def-8123-456789abcdef")
		check(t, err)
		document, err := jsonvalue.Parse([]byte(`{"escaped":"한글","integer":9007199254740993,"null":null}`))
		check(t, err)
		elapsed := duration.FromMicroseconds(-9007199254740993)
		moment := time.Date(2026, time.October, 2, 12, 34, 56, 789123000, time.UTC)
		payload := binaryvalue.Value{Data: string([]byte{0, 255, 128, 10})}
		input := owners.NewBulkScalarCreate("all scalar values").WithAmount(9007199254740993).WithRatio(-1.125).WithCost(cost).WithElapsed(elapsed).WithAt(at).WithDay(day).WithMoment(moment).WithReference(reference).WithPayload(payload).WithDocument(document)
		probe := &bulkProbe{collectionBackend: backend, limits: db.BulkInsertLimits{Rows: 100, Parameters: 12}}
		result, err := owners.BulkScalarObjects.BulkCreateInputs(ctx, probe, orm.CreateInputs[owners.BulkScalar]([]owners.BulkScalarCreate{input, owners.NewBulkScalarCreate("all nullable defaults")}))
		check(t, err)
		if len(probe.plans) != 2 || len(result.Objects) != 2 || !result.Objects[0].Enabled || !result.Objects[1].Enabled {
			t.Fatal("scalar parameter budget/defaults", result, len(probe.plans))
		}
		descriptor := owners.BulkScalarDescriptor{}
		for _, value := range result.Objects {
			stored, err := owners.BulkScalarObjects.Using(backend).Filter(owners.BulkScalarFields.ID.Exact(value.ID)).Get(ctx)
			check(t, err)
			for _, field := range descriptor.Metadata().Fields {
				before, ok := descriptor.WriteFieldValue(value, field)
				after, read := descriptor.WriteFieldValue(stored, field)
				if !ok || !read || !before.Equal(after) {
					t.Fatal("native bulk scalar roundtrip", field.Name, before, after)
				}
			}
		}
		if result.Objects[1].Amount != nil || result.Objects[1].Payload != nil || result.Objects[1].Document != nil {
			t.Fatal("SQL NULL was confused with a scalar zero")
		}
		original := result.Objects[0]
		descriptor.ClearPrimaryKey(&original)
		original.Name, original.Enabled = "raw scalar no defaults", false
		copied, err := owners.BulkScalarObjects.BulkCreate(ctx, backend, []owners.BulkScalar{original})
		check(t, err)
		if copied.Objects[0].Enabled || copied.Objects[0].Payload == original.Payload || copied.Objects[0].Document == original.Document {
			t.Fatal("raw bulk reapplied defaults or borrowed scalar pointers")
		}
		*copied.Objects[0].Payload = binaryvalue.Value{}
		*copied.Objects[0].Document = jsonvalue.Null()
		if original.Payload.Data != payload.Data || original.Document.Text != document.Text {
			t.Fatal("owned scalar result mutated caller")
		}
	})

	t.Run("concurrent_unique_arbitration", func(t *testing.T) {
		other, err := open()
		check(t, err)
		defer func() { check(t, other.Close()) }()
		for _, mode := range []string{"normal", "ignore", "update"} {
			t.Run(mode, func(t *testing.T) {
				type outcome struct {
					value orm.BulkCreateResult[labels.Label]
					err   error
				}
				start := make(chan struct{})
				completed := make(chan outcome, 2)
				name := "concurrent shared " + mode
				for index, connection := range []collectionBackend{backend, other} {
					go func() {
						<-start
						options := []orm.BulkCreateOption[labels.Label]{orm.BulkBatchSize[labels.Label](1)}
						if mode == "ignore" {
							options = append(options, orm.BulkIgnoreConflicts[labels.Label]())
						}
						if mode == "update" {
							options = append(options, orm.BulkUpdateConflictNames[labels.Label]([]string{"name"}, []string{"note"}))
						}
						inputs := []labels.LabelCreate{labels.NewLabelCreate(fmt.Sprint(name, " private ", index)), labels.NewLabelCreate(name).WithNote(fmt.Sprint(index))}
						value, err := labels.LabelObjects.BulkCreateInputs(ctx, connection, orm.CreateInputs[labels.Label](inputs), options...)
						completed <- outcome{value, err}
					}()
				}
				close(start)
				wins, failures, affected := 0, 0, int64(0)
				for range 2 {
					result := <-completed
					if result.err == nil {
						wins++
						affected += result.value.RowsAffected
						if len(result.value.Objects) != 2 || result.value.ReturnedKeys != (mode != "ignore") {
							t.Fatal("concurrent result semantics", result.value)
						}
					} else {
						failures++
						if !errors.Is(result.err, &query.Error{Code: query.CodeUniqueConstraint}) || result.value.Objects != nil {
							t.Fatal("concurrent loser exposed partial result or wrong error", result)
						}
					}
				}
				wantWins, wantFailures, wantAffected, wantRows := 2, 0, int64(4), int64(3)
				if mode == "normal" {
					wantWins, wantFailures, wantAffected, wantRows = 1, 1, 2, 2
				}
				if mode == "ignore" {
					wantAffected = 3
				}
				count, err := labels.LabelObjects.Using(backend).Filter(labels.LabelFields.Name.In(name, name+" private 0", name+" private 1")).Count(ctx)
				check(t, err)
				if wins != wantWins || failures != wantFailures || affected != wantAffected || count != wantRows {
					t.Fatal("native unique arbitration or losing-batch rollback", wins, failures, affected, count)
				}
			})
		}
	})
}
