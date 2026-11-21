package consumer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
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

type bulkUpdateProbe struct {
	collectionBackend
	plans                           []query.BulkUpdatePlan
	transactions, savepoints, limit int
	before, after                   func(int) error
	afterCommit                     func()
}

func (probe *bulkUpdateProbe) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	return probe.collectionBackend.(db.BulkUpdater).BulkUpdateBatchSize(ctx, spec)
}
func (probe *bulkUpdateProbe) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	return probe.collectionBackend.(db.BulkUpdater).BulkUpdate(ctx, plan)
}
func (probe *bulkUpdateProbe) Atomic(ctx context.Context, callback func(db.Session) error) error {
	probe.transactions++
	err := probe.collectionBackend.(db.Atomic).Atomic(ctx, func(session db.Session) error { return callback(bulkUpdateObservedSession{session, probe}) })
	if err == nil && probe.afterCommit != nil {
		probe.afterCommit()
	}
	return err
}

type bulkUpdateObservedSession struct {
	db.Session
	probe *bulkUpdateProbe
}

func (session bulkUpdateObservedSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session bulkUpdateObservedSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	session.probe.savepoints++
	return db.WithSavepoint(ctx, session.Session, func(child db.Session) error { return callback(bulkUpdateObservedSession{child, session.probe}) })
}
func (session bulkUpdateObservedSession) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	limit, err := session.Session.(db.BulkUpdater).BulkUpdateBatchSize(ctx, spec)
	if session.probe.limit > 0 {
		limit = min(limit, session.probe.limit)
	}
	return limit, err
}
func (session bulkUpdateObservedSession) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	index := len(session.probe.plans)
	session.probe.plans = append(session.probe.plans, plan)
	if session.probe.before != nil {
		if err := session.probe.before(index); err != nil {
			return 0, err
		}
	}
	count, err := session.Session.(db.BulkUpdater).BulkUpdate(ctx, plan)
	if err == nil && session.probe.after != nil {
		err = session.probe.after(index)
	}
	return count, err
}

func TestBulkUpdate(t *testing.T) { withCollectionBackends(t, runBulkUpdate) }

func runBulkUpdate(t *testing.T, backend collectionBackend, open func() (collectionBackend, error), exec func(string) error, _ bool) {
	t.Cleanup(func() { check(t, backend.Close()) })
	migrateCollections(t, backend)
	ctx := t.Context()
	api, err := project.Using(backend)
	check(t, err)
	seed := func(t *testing.T, amount int) []labels.Label {
		t.Helper()
		values := make([]labels.Label, amount)
		for i := range values {
			value, err := labels.LabelObjects.Create(ctx, backend, labels.NewLabelCreate(fmt.Sprint(t.Name(), "-", i)).WithNote("original"))
			check(t, err)
			values[i] = value
		}
		return values
	}
	get := func(t *testing.T, key int64) labels.Label {
		t.Helper()
		value, err := labels.LabelObjects.Using(backend).Filter(labels.LabelFields.ID.Exact(key)).Get(ctx)
		check(t, err)
		return value
	}
	mask := orm.BulkUpdateFields(labels.LabelFields.Note)
	t.Run("selected_fields_cache_and_filtered_counts", func(t *testing.T) {
		values := seed(t, 3)
		source := api.LabelsLabel.Filter(labels.LabelFields.ID.In(values[0].ID, values[1].ID)).OrderBy(labels.LabelFields.ID.Desc()).Distinct()
		warm, err := source.All(ctx)
		check(t, err)
		inputs := slices.Clone(values)
		inputs[0].Note, inputs[1].Note, inputs[2].Note = new("updated"), nil, new("excluded")
		for i := range inputs {
			inputs[i].Name = "not selected"
		}
		before := slices.Clone(inputs)
		count, err := source.BulkUpdate(ctx, inputs, mask)
		check(t, err)
		if count != 2 || !reflect.DeepEqual(inputs, before) {
			t.Fatal(count, inputs)
		}
		for i, value := range values {
			stored := get(t, value.ID)
			if stored.Name != value.Name {
				t.Fatal("unselected name changed")
			}
			want := "original"
			if i == 0 {
				want = "updated"
			}
			if i == 1 {
				if stored.Note != nil {
					t.Fatal(stored)
				}
			} else if stored.Note == nil || *stored.Note != want {
				t.Fatal(stored)
			}
		}
		cached, err := source.All(ctx)
		check(t, err)
		if !reflect.DeepEqual(cached, warm) {
			t.Fatal("bulk changed warm query cache")
		}
	})
	t.Run("duplicate_missing_and_batch_counts", func(t *testing.T) {
		value := seed(t, 1)[0]
		first, second := value, value
		first.Note, second.Note = new("first"), new("second")
		for _, size := range []int{2, 1} {
			count, err := api.LabelsLabel.BulkUpdate(ctx, []labels.Label{first, second}, mask, orm.BulkUpdateBatchSize[labels.Label](size))
			check(t, err)
			wantCount, want := int64(1), "first"
			if size == 1 {
				wantCount, want = 2, "second"
			}
			stored := get(t, value.ID)
			if count != wantCount || stored.Note == nil || *stored.Note != want {
				t.Fatal(count, stored)
			}
		}
		missing := labels.NewLabelWithID(1 << 62)
		missing.Note = new("missing")
		if count, err := api.LabelsLabel.BulkUpdate(ctx, []labels.Label{missing}, mask); err != nil || count != 0 {
			t.Fatal("missing update", count, err)
		}
	})
	t.Run("all_inputs_prepared_before_update", func(t *testing.T) {
		values := seed(t, 2)
		values[0].Note = new("must roll back")
		values[1] = labels.Label{ID: values[1].ID, Name: values[1].Name}
		probe := &bulkUpdateProbe{collectionBackend: backend}
		count, err := labels.LabelObjects.BulkUpdate(ctx, probe, values, mask, orm.BulkUpdateBatchSize[labels.Label](1))
		if count != 0 || !errors.Is(err, &query.Error{Code: query.CodeMissingPrimaryKey}) || len(probe.plans) != 0 || *get(t, values[0].ID).Note != "original" {
			t.Fatal(count, err, len(probe.plans))
		}
	})
	t.Run("all_batches_rollback_on_unique_and_late_failure", func(t *testing.T) {
		for _, mode := range []string{"unique", "after_write"} {
			t.Run(mode, func(t *testing.T) {
				original := seed(t, 3)
				values := slices.Clone(original[:2])
				values[0].Name = t.Name() + "-new"
				values[1].Name = original[2].Name
				probe := &bulkUpdateProbe{collectionBackend: backend}
				failure := errors.New("failure after native second update")
				if mode == "after_write" {
					values[1].Name = t.Name() + "-new-second"
					probe.after = func(index int) error {
						if index == 1 {
							return failure
						}
						return nil
					}
				}
				count, err := labels.LabelObjects.BulkUpdate(ctx, probe, values, orm.BulkUpdateFields(labels.LabelFields.Name), orm.BulkUpdateBatchSize[labels.Label](1))
				if count != 0 || err == nil || len(probe.plans) != 2 {
					t.Fatal(count, err, len(probe.plans))
				}
				if mode == "unique" && !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) || mode == "after_write" && !errors.Is(err, failure) {
					t.Fatal(err)
				}
				for _, value := range original {
					if got := get(t, value.ID); !reflect.DeepEqual(got, value) {
						t.Fatal("early update escaped rollback", got, value)
					}
				}
			})
		}
	})
	t.Run("relation_filters_typed_dynamic_and_collection_scope", func(t *testing.T) {
		members := seed(t, 3)
		first, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("bulk-update first"))
		check(t, err)
		second, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("bulk-update second"))
		check(t, err)
		collections, err := project.BindCollections()
		check(t, err)
		links, err := collections.OwnersOwnerLabels.From(backend, first)
		check(t, err)
		check(t, links.Add(ctx, members[:2]))
		other, err := collections.OwnersOwnerLabels.From(backend, second)
		check(t, err)
		check(t, other.Add(ctx, members[:1]))
		r, err := project.BindRelations()
		check(t, err)
		left, right := r.OwnersOwner.Labels.Name.Exact(members[0].Name), r.OwnersOwner.Labels.Name.Exact(members[1].Name)
		values := []owners.Owner{first, second}
		values[0].Name, values[1].Name = "first changed", "must stay second"
		count, err := api.OwnersOwner.Filter(left, right).BulkUpdate(ctx, values, orm.BulkUpdateFields(owners.OwnerFields.Name))
		check(t, err)
		if count != 0 {
			t.Fatal("same filter silently split joins", count)
		}
		for _, dynamic := range []bool{false, true} {
			a, b := left, right
			if dynamic {
				aInputs, err := r.OwnersOwner.ParseDynamic(nil, []orm.LookupInput{{Key: "labels__name", Value: members[0].Name}})
				check(t, err)
				bInputs, err := r.OwnersOwner.ParseDynamic(nil, []orm.LookupInput{{Key: "labels__name", Value: members[1].Name}})
				check(t, err)
				a, b = aInputs[0], bInputs[0]
			}
			count, err := api.OwnersOwner.Filter(a).Filter(b).BulkUpdate(ctx, values, orm.BulkUpdateFieldNames[owners.Owner]("name", "name"))
			check(t, err)
			if count != 1 {
				t.Fatal("separate filter identity lost", count)
			}
		}
		stored, err := owners.OwnerObjects.Using(backend).Filter(owners.OwnerFields.ID.Exact(second.ID)).Get(ctx)
		check(t, err)
		if stored.Name != second.Name {
			t.Fatal("filter widened", stored)
		}
		q, err := links.Query()
		check(t, err)
		warm, err := q.All(ctx)
		check(t, err)
		for i := range members {
			members[i].Note = new("member updated")
		}
		count, err = q.BulkUpdate(ctx, members, mask)
		check(t, err)
		if count != 2 || *get(t, members[2].ID).Note != "original" {
			t.Fatal("collection ownership lost", count)
		}
		cached, err := q.All(ctx)
		check(t, err)
		if !reflect.DeepEqual(cached, warm) {
			t.Fatal("collection cache changed")
		}
	})
	t.Run("eager_prefetch_and_read_lock_shapes", func(t *testing.T) {
		owner, err := owners.OwnerObjects.Create(ctx, backend, owners.NewOwnerCreate("eager bulk owner"))
		check(t, err)
		value, err := owners.RequiredOwnerObjects.Create(ctx, backend, owners.NewRequiredOwnerCreate(owner.ID, 1))
		check(t, err)
		base := api.OwnersRequiredOwner.Filter(owners.RequiredOwnerFields.ID.Exact(value.ID))
		eager := base.SelectRelated(base.Related.Owner)
		warm, err := eager.All(ctx)
		check(t, err)
		value.Amount = 11
		value.OwnerID = 1 << 60
		count, err := eager.BulkUpdate(ctx, []owners.RequiredOwner{value}, orm.BulkUpdateFields(owners.RequiredOwnerFields.Amount))
		check(t, err)
		if count != 1 {
			t.Fatal(count)
		}
		cached, err := eager.All(ctx)
		check(t, err)
		if !reflect.DeepEqual(cached, warm) || cached[0].Amount != 1 {
			t.Fatal("eager cache changed")
		}
		prefetched := base.PrefetchRelated(base.Prefetch.Owner)
		warmPrefetch, err := prefetched.All(ctx)
		check(t, err)
		value.Amount = 22
		count, err = prefetched.BulkUpdate(ctx, []owners.RequiredOwner{value}, orm.BulkUpdateFieldNames[owners.RequiredOwner]("amount"))
		check(t, err)
		if count != 1 {
			t.Fatal(count)
		}
		cachedPrefetch, err := prefetched.All(ctx)
		check(t, err)
		if !reflect.DeepEqual(cachedPrefetch, warmPrefetch) || cachedPrefetch[0].Amount != 11 {
			t.Fatal("prefetch cache changed")
		}
		value.Amount = 33
		count, err = eager.PrefetchRelated(base.Prefetch.Owner).SelectForUpdate(orm.RowLockOptions{}).BulkUpdate(ctx, []owners.RequiredOwner{value}, orm.BulkUpdateFields(owners.RequiredOwnerFields.Amount))
		check(t, err)
		if count != 1 {
			t.Fatal(count)
		}
		current, err := base.Get(ctx)
		check(t, err)
		if current.Amount != 33 || current.OwnerID != owner.ID {
			t.Fatal("read shape/selected values changed mutation", current)
		}
	})
	t.Run("empty_sliced_and_dynamic_validation", func(t *testing.T) {
		sliced, err := api.LabelsLabel.Limit(1)
		check(t, err)
		count, err := sliced.BulkUpdate(ctx, nil, mask)
		if err != nil || count != 0 {
			t.Fatal(count, err)
		}
		value := seed(t, 1)[0]
		count, err = sliced.BulkUpdate(ctx, []labels.Label{value}, mask)
		if count != 0 || !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
			t.Fatal(count, err)
		}
		for _, fields := range [][]string{nil, {"id"}, {"missing"}} {
			count, err := api.LabelsLabel.BulkUpdate(ctx, nil, orm.BulkUpdateFieldNames[labels.Label](fields...))
			if count != 0 || err == nil {
				t.Fatal(count, err)
			}
		}
		if count, err := api.OwnersOwner.BulkUpdate(ctx, nil, orm.BulkUpdateFieldNames[owners.Owner]("labels")); err == nil || count != 0 {
			t.Fatal("columnless relation accepted", count, err)
		}
	})
	t.Run("borrowed_scopes", func(t *testing.T) {
		for _, mode := range []string{"atomic", "coordinated", "relation", "coordinated_relation"} {
			t.Run(mode, func(t *testing.T) {
				for _, finish := range []string{"commit", "rollback"} {
					t.Run(finish, func(t *testing.T) {
						values := seed(t, 3)
						original := slices.Clone(values)
						values[0].Note = new("parent pending")
						rollback := errors.New("parent rolled back")
						var escaped project.LabelsLabelQuery
						err := runSingleScope(ctx, backend, mode, func(session db.Session) error {
							scoped, err := project.UsingSession(session)
							if err != nil {
								return err
							}
							escaped = scoped.LabelsLabel
							count, err := scoped.LabelsLabel.BulkUpdate(ctx, values[:1], mask)
							if err != nil {
								return err
							}
							if count != 1 {
								return errors.New("wrong provisional count")
							}
							bad := slices.Clone(values[:2])
							bad[0].Name = "failed child first " + t.Name()
							bad[1].Name = values[2].Name
							count, err = scoped.LabelsLabel.BulkUpdate(ctx, bad, orm.BulkUpdateFields(labels.LabelFields.Name), orm.BulkUpdateBatchSize[labels.Label](1))
							if count != 0 || !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
								return fmt.Errorf("failed savepoint: %d %w", count, err)
							}
							current, err := scoped.LabelsLabel.Filter(labels.LabelFields.ID.Exact(values[0].ID)).Get(ctx)
							if err != nil {
								return err
							}
							if current.Name != original[0].Name || *current.Note != "parent pending" {
								return errors.New("child changed parent boundary")
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
						for i, value := range original {
							stored := get(t, value.ID)
							want := value
							if finish == "commit" && i == 0 {
								want.Note = new("parent pending")
							}
							if !reflect.DeepEqual(stored, want) {
								t.Fatal("savepoint/parent outcome", stored, want)
							}
						}
						if count, err := escaped.BulkUpdate(ctx, nil, mask); err == nil || count != 0 {
							t.Fatal("expired empty operation", count, err)
						}
					})
				}
			})
		}
	})
	t.Run("root_cursor_affinity", func(t *testing.T) {
		value := seed(t, 1)[0]
		input := value
		input.Note = new("cursor update")
		calls := 0
		err := api.LabelsLabel.Filter(labels.LabelFields.ID.Exact(value.ID)).Iterate(ctx, 1, func(batchContext context.Context, held *project.LabelsLabel) (bool, error) {
			calls++
			count, err := labels.LabelObjects.BulkUpdate(batchContext, backend, []labels.Label{input}, mask)
			if err != nil {
				return false, err
			}
			if count != 1 || *held.Note != "original" {
				return false, errors.New("cursor count/input ownership")
			}
			return false, nil
		})
		check(t, err)
		if calls != 1 || *get(t, value.ID).Note != "cursor update" {
			t.Fatal("cursor mutation lost", calls)
		}
	})
	t.Run("readonly_and_cancellation", func(t *testing.T) {
		check(t, backend.(db.SnapshotReader).ReadSnapshot(ctx, func(reader db.Queryer) error {
			if _, ok := reader.(db.BulkUpdater); ok {
				return errors.New("snapshot advertises bulk update")
			}
			count, err := labels.LabelObjects.BulkUpdate(ctx, reader, nil, mask)
			if count != 0 || !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				return fmt.Errorf("snapshot empty mutation: %w", err)
			}
			return nil
		}))
		values := seed(t, 2)
		for i := range values {
			values[i].Note = new("canceled")
		}
		callCtx, cancel := context.WithCancel(ctx)
		probe := &bulkUpdateProbe{collectionBackend: backend, after: func(int) error { cancel(); return nil }}
		count, err := labels.LabelObjects.BulkUpdate(callCtx, probe, values, mask, orm.BulkUpdateBatchSize[labels.Label](1))
		cancel()
		if count != 0 || !errors.Is(err, context.Canceled) || len(probe.plans) != 1 {
			t.Fatal(count, err, len(probe.plans))
		}
		for _, value := range values {
			if *get(t, value.ID).Note != "original" {
				t.Fatal("canceled batch committed")
			}
		}
		callCtx, cancel = context.WithCancel(ctx)
		probe = &bulkUpdateProbe{collectionBackend: backend, afterCommit: cancel}
		count, err = labels.LabelObjects.BulkUpdate(callCtx, probe, values, mask)
		cancel()
		check(t, err)
		if count != 2 || callCtx.Err() == nil {
			t.Fatal("confirmed count lost after commit", count)
		}
	})
	t.Run("scalar_codecs", func(t *testing.T) { runBulkUpdateScalars(t, backend) })
	t.Run("native_large_batch", func(t *testing.T) {
		var statement strings.Builder
		statement.WriteString("INSERT INTO labels_label (id,name,note) VALUES ")
		values := make([]labels.Label, 1100)
		for i := range values {
			key := int64(1000000 + i)
			values[i] = labels.NewLabelWithID(key)
			values[i].Note = new("large native update")
			if i > 0 {
				statement.WriteString(",")
			}
			fmt.Fprintf(&statement, "(%d,'large-native-%d',NULL)", key, i)
		}
		check(t, exec(statement.String()))
		probe := &bulkUpdateProbe{collectionBackend: backend}
		count, err := labels.LabelObjects.BulkUpdate(ctx, probe, values, mask)
		check(t, err)
		if count != 1100 || len(probe.plans) != 1 || probe.plans[0].ValueCount() != 2200 {
			t.Fatal(count, len(probe.plans))
		}
		stored, err := api.LabelsLabel.Filter(labels.LabelFields.Note.Exact("large native update")).Count(ctx)
		check(t, err)
		if stored != 1100 {
			t.Fatal(stored)
		}
	})
	t.Run("two_connection_atomicity", func(t *testing.T) {
		values := seed(t, 4)
		other, err := open()
		check(t, err)
		defer func() { check(t, other.Close()) }()
		type outcome struct {
			count int64
			err   error
		}
		start, done := make(chan struct{}), make(chan outcome, 2)
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		for i, connection := range []collectionBackend{backend, other} {
			go func() {
				<-start
				input := slices.Clone(values[i*2 : i*2+2])
				input[0].Name = fmt.Sprint(t.Name(), "-private-", i)
				input[1].Name = t.Name() + "-shared"
				count, err := labels.LabelObjects.BulkUpdate(callCtx, connection, input, orm.BulkUpdateFields(labels.LabelFields.Name), orm.BulkUpdateBatchSize[labels.Label](1))
				done <- outcome{count, err}
			}()
		}
		close(start)
		wins, losses := 0, 0
		for range 2 {
			result := <-done
			if result.err == nil {
				wins++
				if result.count != 2 {
					t.Fatal(result)
				}
			} else {
				losses++
				if result.count != 0 || !errors.Is(result.err, &query.Error{Code: query.CodeUniqueConstraint}) {
					t.Fatal(result)
				}
			}
		}
		if wins != 1 || losses != 1 {
			t.Fatal(wins, losses)
		}
		count, err := api.LabelsLabel.Filter(labels.LabelFields.Name.In(values[0].Name, values[1].Name, values[2].Name, values[3].Name)).Count(ctx)
		check(t, err)
		if count != 2 {
			t.Fatal("losing transaction retained partial change", count)
		}
	})
}

func runBulkUpdateScalars(t *testing.T, backend collectionBackend) {
	ctx := t.Context()
	cost, err := decimal.Parse("-12345.67")
	check(t, err)
	at, err := clock.Parse("12:34:56.789123")
	check(t, err)
	day, err := calendar.Parse("2024-02-29")
	check(t, err)
	reference, err := uuid.Parse("12345678-9abc-4def-8123-456789abcdef")
	check(t, err)
	document, err := jsonvalue.Parse([]byte(`{"n":9007199254740993,"null":null}`))
	check(t, err)
	first, err := owners.BulkScalarObjects.Create(ctx, backend, owners.NewBulkScalarCreate("update scalar one"))
	check(t, err)
	second, err := owners.BulkScalarObjects.Create(ctx, backend, owners.NewBulkScalarCreate("update scalar two"))
	check(t, err)
	first.Enabled = false
	first.Amount = new(int64(9007199254740993))
	first.Ratio = new(-1.125)
	first.Cost = &cost
	first.Elapsed = new(duration.FromMicroseconds(-9007199254740993))
	first.At = &at
	first.Day = &day
	first.Moment = new(time.Date(2026, 10, 2, 12, 34, 56, 789123000, time.UTC))
	first.Reference = &reference
	first.Payload = new(binaryvalue.Value{Data: string([]byte{0, 255, 128, 10})})
	first.Document = &document
	fields := []string{"enabled", "amount", "ratio", "cost", "elapsed", "at", "day", "moment", "reference", "payload", "document"}
	mask := orm.BulkUpdateFieldNames[owners.BulkScalar](fields...)
	values := []owners.BulkScalar{first, second}
	probe := &bulkUpdateProbe{collectionBackend: backend, limit: 1}
	count, err := owners.BulkScalarObjects.BulkUpdate(ctx, probe, values, mask)
	check(t, err)
	if count != 2 || len(probe.plans) != 2 {
		t.Fatal(count, len(probe.plans))
	}
	descriptor := owners.BulkScalarDescriptor{}
	for _, value := range values {
		stored, err := owners.BulkScalarObjects.Using(backend).Filter(owners.BulkScalarFields.ID.Exact(value.ID)).Get(ctx)
		check(t, err)
		for _, field := range descriptor.Metadata().Fields {
			want, ok := descriptor.WriteFieldValue(value, field)
			got, read := descriptor.WriteFieldValue(stored, field)
			if !ok || !read || !want.Equal(got) {
				t.Fatal("generated bulk update codec", field.Name, want, got)
			}
		}
	}
	bad := slices.Clone(values)
	bad[0].Document = new(jsonvalue.Null())
	bad[1].Document = new(jsonvalue.Value{Text: "{"})
	probe = &bulkUpdateProbe{collectionBackend: backend}
	count, err = owners.BulkScalarObjects.BulkUpdate(ctx, probe, bad, mask, orm.BulkUpdateBatchSize[owners.BulkScalar](1))
	if err == nil || count != 0 || len(probe.plans) != 0 {
		t.Fatal("invalid late JSON escaped preparation", count, err, len(probe.plans))
	}
	first = owners.NewBulkScalarWithID(first.ID)
	first.Name = "unselected name"
	count, err = owners.BulkScalarObjects.BulkUpdate(ctx, backend, []owners.BulkScalar{first}, mask)
	check(t, err)
	if count != 1 {
		t.Fatal(count)
	}
	stored, err := owners.BulkScalarObjects.Using(backend).Filter(owners.BulkScalarFields.ID.Exact(first.ID)).Get(ctx)
	check(t, err)
	if stored.Name != "update scalar one" || stored.Enabled || stored.Amount != nil || stored.Document != nil || stored.Payload != nil {
		t.Fatal("all-NULL update/selected mask", stored)
	}
}
