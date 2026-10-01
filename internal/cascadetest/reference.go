package cascadetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	fixture "github.com/progresshans/godj/conformance/cascadefixture"
	"github.com/progresshans/godj/conformance/cascadefixture/details"
	"github.com/progresshans/godj/conformance/cascadefixture/parents"
	"github.com/progresshans/godj/conformance/cascadefixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/migrationautodetect"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type ProductBackend interface {
	db.Session
	db.Atomic
	db.RelationAtomic
	mb.RevisionFencedBackend
	Close() error
}

type referenceModel struct {
	label string
	model ir.Model
}

type referenceSnapshot map[string][][]any

// RunReference exercises the real generated deleters against independently
// captured Django outcomes. Only the required-cycle fixture seed uses native
// SQL, because GoDj does not support assigning a generated key on insertion.
func RunReference(t *testing.T, backend ProductBackend, dialect string, seedRequiredCycle func(context.Context) error) {
	t.Helper()
	ctx := t.Context()
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	spec, err := fixture.ProjectSpec(ctx)
	checkReference(t, err)
	var schemas []ir.Schema
	var models []referenceModel
	for _, app := range spec.Apps {
		schemas = append(schemas, app.Schema)
		for _, model := range app.Schema.Models {
			label := app.Schema.AppLabel + "." + model.GoName
			if model.Name == "root_labels" {
				label = "cascadeparents.Root_labels"
			}
			models = append(models, referenceModel{label, model})
		}
	}
	desired, err := migrations.NewProjectState(schemas...)
	checkReference(t, err)
	empty, _, err := definition.Load()
	checkReference(t, err)
	plan, err := migrationautodetect.Detect(migrationautodetect.Request{Definitions: empty, Desired: desired, ManagedApps: desired.Apps()})
	checkReference(t, err)
	var sources []definition.Source
	for _, migration := range plan.Migrations() {
		wire, err := definition.Encode(definition.Producer{Name: "cascade-product", Version: "1"}, migration)
		checkReference(t, err)
		sources = append(sources, definition.Source{SourceID: migration.App + "/" + migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	checkReference(t, err)
	_, err = (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	checkReference(t, err)
	deleters, err := project.BindRelationDeleters()
	checkReference(t, err)
	data, err := os.ReadFile(filepath.Join("..", "..", "orm", "testdata", "cascade-django61-"+dialect+".json"))
	checkReference(t, err)
	var reference struct {
		Django       string                     `json:"django"`
		Observations map[string]json.RawMessage `json:"observations"`
	}
	checkReference(t, json.Unmarshal(data, &reference))
	if reference.Django != "6.1" || len(reference.Observations) != 13 {
		t.Fatal("wrong independent CASCADE reference inventory")
	}
	snapshot := func(t *testing.T) referenceSnapshot { return readReferenceSnapshot(t, ctx, backend, models) }
	root := func(t *testing.T, name string) parents.Root {
		value, err := parents.RootObjects.Create(ctx, backend, parents.NewRootCreate(name))
		checkReference(t, err)
		return value
	}
	tree := func(t *testing.T) (parents.Root, details.Child, details.Grandchild, details.Watcher) {
		r := root(t, "root")
		c, err := details.ChildObjects.Create(ctx, backend, details.NewChildCreate(r.ID))
		checkReference(t, err)
		g, err := details.GrandchildObjects.Create(ctx, backend, details.NewGrandchildCreate(c.ID))
		checkReference(t, err)
		w, err := details.WatcherObjects.Create(ctx, backend, details.NewWatcherCreate().WithChildID(c.ID))
		checkReference(t, err)
		return r, c, g, w
	}
	counts := func(t *testing.T) map[string]int { return referenceCounts(snapshot(t)) }
	deleted := func(t *testing.T, before referenceSnapshot, total int64, err error) map[string]any {
		checkReference(t, err)
		return referenceDeleted(before, snapshot(t), total)
	}
	check := func(t *testing.T, name string, observation any) {
		encoded, err := json.Marshal(observation)
		checkReference(t, err)
		var got, want any
		checkReference(t, json.Unmarshal(encoded, &got))
		checkReference(t, json.Unmarshal(reference.Observations[name], &want))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s differs from independent Django\ngot %s\nwant %s", name, encoded, reference.Observations[name])
		}
	}
	clear := func(t *testing.T) {
		before := snapshot(t)
		checkReference(t, backend.AtomicRelation(ctx, func(session db.RelationSession) error {
			for index := len(models) - 1; index >= 0; index-- {
				model := models[index]
				for _, row := range before[model.label] {
					count, err := session.Delete(ctx, query.NewDeletePlan(model.model.DBTable, referenceKey(), query.Integer(row[0].(int64))))
					if err != nil {
						return err
					}
					if count != 1 {
						return fmt.Errorf("clear fixture removed %d rows", count)
					}
				}
			}
			return nil
		}))
		if len(counts(t)) != 0 {
			t.Fatal("reference case leaked fixture rows")
		}
	}
	cases := []struct {
		name string
		run  func(*testing.T) any
	}{
		{"recursive_set_null", func(t *testing.T) any {
			r, c, _, w := tree(t)
			other := root(t, "other")
			otherChild, err := details.ChildObjects.Create(ctx, backend, details.NewChildCreate(other.ID))
			checkReference(t, err)
			before := snapshot(t)
			total, err := deleters.ParentsRoot.Delete(ctx, backend, &r)
			got := deleted(t, before, total, err)
			stored, err := details.WatcherObjects.Using(backend).Filter(details.WatcherFields.ID.Exact(w.ID)).All(ctx)
			checkReference(t, err)
			_, rootPresent := (parents.RootDescriptor{}).PrimaryKey(r)
			_, childPresent := (details.ChildDescriptor{}).PrimaryKey(c)
			got["root_pk_cleared"] = !rootPresent && r.ID == 0
			got["retained_child_instance_pk"] = childPresent
			got["watcher_database_null"] = len(stored) == 1 && stored[0].ChildID == nil
			got["watcher_held_value_preserved"] = w.ChildID != nil && *w.ChildID == c.ID
			got["unrelated_child_preserved"] = hasReferenceKey(snapshot(t)["cascadedetails.Child"], otherChild.ID)
			got["counts"] = counts(t)
			return got
		}},
		{"protected_descendant", func(t *testing.T) any {
			r, _, g, _ := tree(t)
			_, err := details.ProtectedObjects.Create(ctx, backend, details.NewProtectedCreate(g.ID))
			checkReference(t, err)
			before, key := snapshot(t), r.ID
			observed := &referenceAtomic{RelationAtomic: backend}
			total, err := deleters.ParentsRoot.Delete(ctx, observed, &r)
			var protected *query.ProtectedForeignKeyError
			if total != 0 || !errors.As(err, &protected) {
				t.Fatal("protected descendant was not rejected", total, err)
			}
			return map[string]any{"protected_count": protected.ProtectedSourceRows(), "writes": observed.writes, "rows_preserved": reflect.DeepEqual(before, snapshot(t)), "caller_pk_preserved": r.ID == key}
		}},
		{"protect_cascade_overlap", func(t *testing.T) any {
			r := root(t, "overlap")
			_, err := details.OverlapObjects.Create(ctx, backend, details.NewOverlapCreate(r.ID, r.ID))
			checkReference(t, err)
			before := snapshot(t)
			total, err := deleters.ParentsRoot.Delete(ctx, backend, &r)
			var protected *query.ProtectedForeignKeyError
			if total != 0 || !errors.As(err, &protected) {
				t.Fatal("CASCADE bypassed PROTECT", total, err)
			}
			return map[string]any{"protected_count": protected.ProtectedSourceRows(), "rows_preserved": reflect.DeepEqual(before, snapshot(t))}
		}},
		{"duplicate_paths", func(t *testing.T) any {
			r := root(t, "twice")
			_, err := details.TwinObjects.Create(ctx, backend, details.NewTwinCreate(r.ID, r.ID))
			checkReference(t, err)
			before := snapshot(t)
			total, err := deleters.ParentsRoot.Delete(ctx, backend, &r)
			return deleted(t, before, total, err)
		}},
		{"hidden_and_one_to_one", func(t *testing.T) any {
			r := root(t, "hidden-and-one-to-one")
			_, err := details.HiddenObjects.Create(ctx, backend, details.NewHiddenCreate(r.ID))
			checkReference(t, err)
			_, err = details.DetailObjects.Create(ctx, backend, details.NewDetailCreate(r.ID))
			checkReference(t, err)
			before := snapshot(t)
			total, err := deleters.ParentsRoot.Delete(ctx, backend, &r)
			return deleted(t, before, total, err)
		}},
		{"self_loop", func(t *testing.T) any {
			n, err := parents.NodeObjects.Create(ctx, backend, parents.NewNodeCreate())
			checkReference(t, err)
			n, err = parents.NodeObjects.Update(ctx, backend, n, parents.NodePatch{}.WithParentID(n.ID))
			checkReference(t, err)
			before := snapshot(t)
			total, err := deleters.ParentsNode.Delete(ctx, backend, &n)
			return deleted(t, before, total, err)
		}},
		{"same_model_cycle", func(t *testing.T) any {
			n, err := parents.NodeObjects.Create(ctx, backend, parents.NewNodeCreate())
			checkReference(t, err)
			other, err := parents.NodeObjects.Create(ctx, backend, parents.NewNodeCreate().WithParentID(n.ID))
			checkReference(t, err)
			n, err = parents.NodeObjects.Update(ctx, backend, n, parents.NodePatch{}.WithParentID(other.ID))
			checkReference(t, err)
			before := snapshot(t)
			total, err := deleters.ParentsNode.Delete(ctx, backend, &n)
			return deleted(t, before, total, err)
		}},
		{"nullable_cascade", func(t *testing.T) any {
			n, err := parents.NodeObjects.Create(ctx, backend, parents.NewNodeCreate())
			checkReference(t, err)
			_, err = parents.NodeObjects.Create(ctx, backend, parents.NewNodeCreate().WithParentID(n.ID))
			checkReference(t, err)
			other, err := parents.NodeObjects.Create(ctx, backend, parents.NewNodeCreate())
			checkReference(t, err)
			before := snapshot(t)
			total, err := deleters.ParentsNode.Delete(ctx, backend, &n)
			got := deleted(t, before, total, err)
			got["unrelated_null_preserved"] = hasReferenceKey(snapshot(t)["cascadeparents.Node"], other.ID)
			return got
		}},
		{"cross_app_cycle", func(t *testing.T) any {
			left, err := parents.LeftObjects.Create(ctx, backend, parents.NewLeftCreate())
			checkReference(t, err)
			right, err := details.RightObjects.Create(ctx, backend, details.NewRightCreate(left.ID))
			checkReference(t, err)
			left, err = parents.LeftObjects.Update(ctx, backend, left, parents.LeftPatch{}.WithRightID(right.ID))
			checkReference(t, err)
			before := snapshot(t)
			total, err := deleters.ParentsLeft.Delete(ctx, backend, &left)
			return deleted(t, before, total, err)
		}},
		{"required_cross_app_cycle", func(t *testing.T) any {
			checkReference(t, seedRequiredCycle(ctx))
			left := parents.RequiredLeft{RightID: 202}
			(parents.RequiredLeftDescriptor{}).SetPrimaryKey(&left, 101)
			before := snapshot(t)
			total, err := deleters.ParentsRequiredLeft.Delete(ctx, backend, &left)
			return deleted(t, before, total, err)
		}},
		{"late_failure", func(t *testing.T) any {
			r, _, _, _ := tree(t)
			before, key := snapshot(t), r.ID
			failure := errors.New("late root delete failure")
			observed := &referenceAtomic{RelationAtomic: backend, failRoot: failure}
			total, err := deleters.ParentsRoot.Delete(ctx, observed, &r)
			if total != 0 || !errors.Is(err, failure) {
				t.Fatal("late failure did not retain its cause", total, err)
			}
			return map[string]any{"prior_mutation_executed": observed.changed > 0, "rows_preserved": reflect.DeepEqual(before, snapshot(t)), "caller_pk_preserved": r.ID == key}
		}},
		{"raw_delete", func(t *testing.T) any {
			r, _, _, _ := tree(t)
			before := snapshot(t)
			err := backend.AtomicRelation(ctx, func(session db.RelationSession) error {
				_, err := session.Delete(ctx, query.NewDeletePlan("cascade_root", referenceKey(), query.Integer(r.ID)))
				return err
			})
			valid := false
			if dialect == "sqlite" {
				var cause *sqlitedriver.Error
				valid = errors.As(err, &cause) && cause.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
			} else {
				var cause *pgconn.PgError
				valid = errors.As(err, &cause) && cause.Code == "23503"
			}
			return map[string]any{"integrity_error": valid, "rows_preserved": reflect.DeepEqual(before, snapshot(t))}
		}},
		{"many_to_many_cleanup", func(t *testing.T) any {
			label, err := parents.LabelObjects.Create(ctx, backend, parents.NewLabelCreate("shared"))
			checkReference(t, err)
			second, err := parents.LabelObjects.Create(ctx, backend, parents.NewLabelCreate("second"))
			checkReference(t, err)
			r, other := root(t, "first"), root(t, "second")
			for _, pair := range [][2]int64{{r.ID, label.ID}, {r.ID, second.ID}, {other.ID, label.ID}} {
				_, err := parents.RootLabelsObjects.Create(ctx, backend, parents.NewRootLabelsCreate(pair[0], pair[1]))
				checkReference(t, err)
			}
			// The explicit association has native uniqueness. General add/remove
			// manager semantics remain outside this fixture's product claim.
			_, err = parents.RootLabelsObjects.Create(ctx, backend, parents.NewRootLabelsCreate(r.ID, label.ID))
			if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
				t.Fatal("duplicate association did not hit its unique constraint", err)
			}
			initial := counts(t)["cascadeparents.Root_labels"]
			before := snapshot(t)
			total, err := deleters.ParentsRoot.Delete(ctx, backend, &r)
			rootResult := deleted(t, before, total, err)
			after := snapshot(t)
			members := []string{}
			for _, link := range after["cascadeparents.Root_labels"] {
				if link[1] == other.ID {
					for _, row := range after["cascadeparents.Label"] {
						if row[0] == link[2] {
							members = append(members, row[1].(string))
						}
					}
				}
			}
			afterRoot := map[string]any{"labels": len(after["cascadeparents.Label"]), "links": len(after["cascadeparents.Root_labels"]), "other_members": members}
			before = snapshot(t)
			total, err = deleters.ParentsLabel.Delete(ctx, backend, &label)
			labelResult := deleted(t, before, total, err)
			after = snapshot(t)
			remaining := []string{}
			for _, row := range after["cascadeparents.Label"] {
				remaining = append(remaining, row[1].(string))
			}
			return map[string]any{"initial_unique_links": initial, "root_delete": rootResult, "after_root": afterRoot, "label_delete": labelResult,
				"other_root_preserved": hasReferenceKey(after["cascadeparents.Root"], other.ID), "remaining_links": len(after["cascadeparents.Root_labels"]), "remaining_labels": remaining}
		}},
	}
	if len(cases) != len(reference.Observations) {
		t.Fatal("missing CASCADE observation")
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) { t.Cleanup(func() { clear(t) }); check(t, test.name, test.run(t)) })
	}
	// These execution-boundary checks are Go-native additions, not observations
	// imported from the Django fixture above.
	for _, mode := range []string{"commit_unknown", "rollback_unknown", "cancel_after_delete"} {
		t.Run("go_native_"+mode, func(t *testing.T) {
			t.Cleanup(func() { clear(t) })
			r, _, _, _ := tree(t)
			before, caller := snapshot(t), r
			callContext, cancel := context.WithCancel(ctx)
			defer cancel()
			failure := errors.New("native boundary " + mode)
			observed := &referenceAtomic{RelationAtomic: backend}
			switch mode {
			case "commit_unknown":
				observed.afterAtomic = &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Cause: failure}
			case "rollback_unknown":
				observed.failRoot = failure
				observed.afterAtomic = &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown, Cause: failure}
			case "cancel_after_delete":
				observed.afterDelete = func(table string) {
					if table == "cascade_grandchild" {
						cancel()
					}
				}
			}
			count, err := deleters.ParentsRoot.Delete(callContext, observed, &r)
			if count != 0 || err == nil || observed.calls != 1 || r != caller || observed.changed == 0 {
				t.Fatal("uncertain/canceled native graph published, retried or did not execute its prefix", count, err, observed.calls, observed.changed)
			}
			if mode == "cancel_after_delete" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("late native cancellation lost its cause", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatal("uncertain native outcome lost its cause", err)
			}
			after := snapshot(t)
			if mode == "commit_unknown" {
				// The wrapper reports uncertainty after an actual native commit.
				// The caller cannot infer rollback or safely retry from that error.
				if hasReferenceKey(after["cascadeparents.Root"], caller.ID) || len(after["cascadedetails.Child"]) != 0 || len(after["cascadedetails.Grandchild"]) != 0 ||
					len(after["cascadedetails.Watcher"]) != 1 || after["cascadedetails.Watcher"][0][1] != nil {
					t.Fatal("uncertain report did not follow a real completed graph commit")
				}
			} else if !reflect.DeepEqual(before, after) {
				t.Fatal("failed native graph did not roll back its mutation prefix")
			}
		})
	}
}

func checkReference(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func referenceKey() query.FieldRef { return query.NewFieldRef("id", "id", query.FieldInteger, false) }
func hasReferenceKey(rows [][]any, key int64) bool {
	for _, row := range rows {
		if row[0] == key {
			return true
		}
	}
	return false
}
func referenceCounts(snapshot referenceSnapshot) map[string]int {
	counts := map[string]int{}
	for label, rows := range snapshot {
		if len(rows) != 0 {
			counts[label] = len(rows)
		}
	}
	return counts
}
func referenceDeleted(before, after referenceSnapshot, total int64) map[string]any {
	counts := map[string]int{}
	for label, rows := range before {
		if difference := len(rows) - len(after[label]); difference != 0 {
			counts[label] = difference
		}
	}
	return map[string]any{"total": total, "models": counts}
}

func readReferenceSnapshot(t testing.TB, ctx context.Context, backend db.Queryer, models []referenceModel) referenceSnapshot {
	t.Helper()
	snapshot := referenceSnapshot{}
	for _, model := range models {
		fields := make([]query.FieldRef, len(model.model.Fields))
		for index, field := range model.model.Fields {
			kind := query.FieldInteger
			if field.Kind == ir.FieldChar {
				kind = query.FieldString
			}
			fields[index] = query.NewFieldRef(field.Name, field.Column, kind, field.Nullable)
		}
		rows, err := backend.Query(ctx, query.NewPlan(model.model.DBTable, fields).WithOrderings(query.NewOrdering(referenceKey(), query.Ascending)))
		checkReference(t, err)
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(fields))
			dest := make([]any, len(fields))
			for index := range row {
				dest[index] = &row[index]
			}
			if err := rows.Scan(dest...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			values = append(values, row)
		}
		err = rows.Err()
		checkReference(t, errors.Join(err, rows.Close()))
		snapshot[model.label] = values
	}
	return snapshot
}

type referenceAtomic struct {
	db.RelationAtomic
	writes      int
	changed     int64
	failRoot    error
	calls       int
	afterAtomic error
	afterDelete func(string)
}
type referenceSession struct {
	db.RelationSession
	owner *referenceAtomic
}

func (b *referenceAtomic) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	b.calls++
	err := b.RelationAtomic.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(&referenceSession{session, b}) })
	return errors.Join(err, b.afterAtomic)
}
func (s *referenceSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if plan.Table() == "cascade_root" && s.owner.failRoot != nil {
		return 0, s.owner.failRoot
	}
	s.owner.writes++
	count, err := s.RelationSession.Delete(ctx, plan)
	if err == nil {
		s.owner.changed += count
		if s.owner.afterDelete != nil {
			s.owner.afterDelete(plan.Table())
		}
	}
	return count, err
}
func (s *referenceSession) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	s.owner.writes++
	count, err := s.RelationSession.RelationSetNull(ctx, plan)
	if err == nil {
		s.owner.changed += count
	}
	return count, err
}
