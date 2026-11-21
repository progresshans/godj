package consumer

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"example.com/godj-single-object-reference/models"
	"example.com/godj-single-object-reference/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

//go:embed django61-*.json
var references embed.FS

type nativeReference struct {
	Kind, Django, Python, Backend string
	Sources                       map[string]string `json:"source_sha256"`
	Get                           map[string]struct{ Outcome json.RawMessage }
	Creation                      map[string]json.RawMessage
	Race                          json.RawMessage
}

func loadReference(t *testing.T, backend string) nativeReference {
	t.Helper()
	data, err := references.ReadFile("django61-" + backend + ".json")
	check(t, err)
	var fixture nativeReference
	check(t, json.Unmarshal(data, &fixture))
	wantBackend := backend
	if backend == "postgres" {
		wantBackend = "postgresql"
	}
	if fixture.Kind != "django-single-object-reference-v1" || fixture.Django != "6.1" || fixture.Python != "3.14.3" || fixture.Backend != wantBackend ||
		fixture.Sources["queryset"] != "5e86af673328a1d6800342f4bec9d4e2352b7919bebef349d98962d67569eae6" || fixture.Sources["transaction"] != "aee995a682da7157a7ef415521e4d3336c25fd5a25c1b8582e2453c5f1d30a79" || len(fixture.Get) != 11 || len(fixture.Creation) != 9 {
		t.Fatal("unexpected native fixture provenance or inventory")
	}
	return fixture
}
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func compare(t *testing.T, expected json.RawMessage, actual any) {
	t.Helper()
	if len(expected) == 0 {
		t.Fatal("missing native observation")
	}
	encoded, err := json.Marshal(actual)
	check(t, err)
	var want, got any
	check(t, json.Unmarshal(expected, &want))
	check(t, json.Unmarshal(encoded, &got))
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("native=%s Go=%s", expected, encoded)
	}
}
func referenceError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}):
		return "DoesNotExist"
	case errors.Is(err, &query.Error{Code: query.CodeMultipleObjectsReturned}):
		return "MultipleObjectsReturned"
	case errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField}):
		return "FieldError"
	case errors.Is(err, &query.Error{Category: query.CategoryIntegrity}):
		return "IntegrityError"
	default:
		return "unexpected: " + err.Error()
	}
}

type referenceBackend interface {
	db.Session
	db.Atomic
	migrationbackend.RevisionFencedBackend
	Close() error
}
type referenceTrace struct {
	commands []string
	depth    int
}

func (trace *referenceTrace) take() []string {
	result := append([]string{}, trace.commands...)
	trace.commands = nil
	return result
}

type tracedReferenceRoot struct {
	referenceBackend
	trace *referenceTrace
}

func (root tracedReferenceRoot) Atomic(ctx context.Context, callback func(db.Session) error) error {
	err := root.referenceBackend.Atomic(ctx, func(session db.Session) error {
		root.trace.commands = append(root.trace.commands, "BEGIN")
		root.trace.depth++
		defer func() { root.trace.depth-- }()
		return callback(tracedReferenceSession{Session: session, trace: root.trace})
	})
	if err == nil {
		root.trace.commands = append(root.trace.commands, "COMMIT")
	} else {
		root.trace.commands = append(root.trace.commands, "ROLLBACK")
	}
	return err
}

type tracedReferenceSession struct {
	db.Session
	trace *referenceTrace
}

func (session tracedReferenceSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session tracedReferenceSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	err := db.WithSavepoint(ctx, session.Session, func(child db.Session) error {
		session.trace.commands = append(session.trace.commands, "SAVEPOINT")
		session.trace.depth++
		defer func() { session.trace.depth-- }()
		return callback(tracedReferenceSession{Session: child, trace: session.trace})
	})
	if err != nil {
		session.trace.commands = append(session.trace.commands, "ROLLBACK TO SAVEPOINT")
	}
	session.trace.commands = append(session.trace.commands, "RELEASE SAVEPOINT")
	return err
}

type singleCreate[M any] func() orm.Mutation[M]

func (build singleCreate[M]) BuildCreate() orm.Mutation[M] { return build() }

func TestSingleObjectReference(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "reference.sqlite3"))
		check(t, err)
		runReference(t, backend, "sqlite")
	})
	raw := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if raw == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL is absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		admin, err := pgx.Connect(t.Context(), raw)
		check(t, err)
		name := fmt.Sprintf("godj_single_reference_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		_, err = admin.Exec(t.Context(), "CREATE SCHEMA "+quoted)
		check(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			check(t, err)
			check(t, admin.Close(ctx))
		})
		backend, err := postgres.Open(t.Context(), postgres.Config{URL: raw, Schema: name})
		check(t, err)
		runReference(t, backend, "postgres")
	})
}
func runReference(t *testing.T, backend referenceBackend, kind string) {
	t.Helper()
	t.Cleanup(func() { check(t, backend.Close()) })
	fixture := loadReference(t, kind)
	migration := migrations.Migration{App: "singleobject", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "singleobject", Model: (models.CategoryDescriptor{}).Metadata()},
		migrations.CreateModel{AppLabel: "singleobject", Model: (models.LabelDescriptor{}).Metadata()},
	}}
	wire, err := definition.Encode(definition.Producer{Name: "single-object-reference", Version: "1"}, migration)
	check(t, err)
	loaded, _, err := definition.Load(definition.Source{SourceID: "single-object-reference", Document: wire})
	check(t, err)
	_, err = (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	check(t, err)
	t.Run("get", func(t *testing.T) { compareReferenceGet(t, backend, fixture) })
	// The two independent observer groups start with empty tables. Delete with
	// the public key-aware API, retaining the native FK/unique constraints.
	labels, err := models.LabelObjects.Using(backend).All(t.Context())
	check(t, err)
	for _, label := range labels {
		_, err := models.LabelObjects.Delete(t.Context(), backend, &label)
		check(t, err)
	}
	categories, err := models.CategoryObjects.Using(backend).All(t.Context())
	check(t, err)
	for _, category := range categories {
		_, err := models.CategoryObjects.Delete(t.Context(), backend, &category)
		check(t, err)
	}
	t.Run("creation", func(t *testing.T) { compareReferenceCreation(t, backend, fixture) })
}

func compareReferenceGet(t *testing.T, backend referenceBackend, fixture nativeReference) {
	ctx := t.Context()
	api, err := project.Using(backend)
	check(t, err)
	parent, err := models.CategoryObjects.Create(ctx, backend, models.NewCategoryCreate("support"))
	check(t, err)
	single, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("single", parent.ID).WithDetail("original"))
	check(t, err)
	observe := func(name string, source project.ModelsLabelQuery) {
		t.Run(name, func(t *testing.T) {
			value, err := source.Get(ctx)
			var outcome map[string]any
			if err != nil {
				outcome = map[string]any{"error": referenceError(err)}
				if errors.Is(err, &query.Error{Code: query.CodeMultipleObjectsReturned}) {
					outcome["more_than_twenty"] = strings.Contains(err.Error(), "more than 20")
				}
			} else {
				outcome = map[string]any{"name": value.Name, "detail": value.Detail}
			}
			compare(t, fixture.Get[name].Outcome, outcome)
		})
	}
	base := api.ModelsLabel
	observe("single", base.Filter(models.LabelFields.Name.Exact("single")))
	observe("missing", base.Filter(models.LabelFields.Name.Exact("absent")))
	for _, suffix := range []string{"a", "b", "c"} {
		_, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("several-"+suffix, parent.ID).WithDetail(suffix))
		check(t, err)
	}
	several := base.Filter(models.LabelFields.Name.In("several-a", "several-b", "several-c"))
	observe("multiple", several)
	warm := base.Filter(models.LabelFields.Name.Exact("single"))
	cached, err := warm.All(ctx)
	check(t, err)
	_, err = models.LabelObjects.Patch(ctx, backend, single, models.LabelPatch{}.WithDetail("updated"))
	check(t, err)
	t.Run("warm_get_refresh", func(t *testing.T) {
		refreshed, err := warm.Get(ctx)
		check(t, err)
		after, err := warm.All(ctx)
		check(t, err)
		compare(t, fixture.Get["warm_get_refresh"].Outcome, map[string]any{"cached": cached[0].Detail, "returned": refreshed.Detail, "cache_after": after[0].Detail, "shares_cached_object": refreshed == cached[0]})
	})
	_, err = models.LabelObjects.Delete(ctx, backend, &single)
	check(t, err)
	observe("warm_get_after_delete", warm)
	observe("unsliced_order_removed", base.Filter(models.LabelFields.Name.Exact("several-a")).OrderBy(models.LabelFields.Name.Desc()))
	ordered := several.OrderBy(models.LabelFields.Name.Desc())
	first, err := ordered.Limit(1)
	check(t, err)
	observe("slice_one", first)
	offset, err := ordered.Offset(1)
	check(t, err)
	offset, err = offset.Limit(1)
	check(t, err)
	observe("slice_offset", offset)
	empty, err := base.Limit(0)
	check(t, err)
	observe("empty_slice", empty)
	observe("empty_predicate", base.Filter(models.LabelFields.Name.In()))
	var many []string
	for index := range 22 {
		name := fmt.Sprintf("many-%02d", index)
		many = append(many, name)
		_, err := models.LabelObjects.Create(ctx, backend, models.NewLabelCreate(name, parent.ID))
		check(t, err)
	}
	observe("many_matches", base.Filter(models.LabelFields.Name.In(many...)))
}

func compareReferenceCreation(t *testing.T, backend referenceBackend, fixture nativeReference) {
	ctx := t.Context()
	trace := &referenceTrace{}
	root := tracedReferenceRoot{referenceBackend: backend, trace: trace}
	parent, err := models.CategoryObjects.Create(ctx, root, models.NewCategoryCreate("support"))
	check(t, err)
	relations, err := project.BindRelations()
	check(t, err)
	lookup := func(backend db.Queryer, name string) orm.QuerySet[models.Label] {
		return models.LabelObjects.Using(backend).Filter(models.LabelFields.Name.Exact(name), relations.ModelsLabel.Category.ID.Exact(parent.ID))
	}
	exists := func(name string) bool {
		count, err := models.LabelObjects.Using(root).Filter(models.LabelFields.Name.Exact(name)).Count(ctx)
		check(t, err)
		return count != 0
	}
	calls := []map[string]bool{}
	input := singleCreate[models.Label](func() orm.Mutation[models.Label] {
		calls = append(calls, map[string]bool{"in_atomic_block": trace.depth > 0})
		return models.NewLabelCreate("once", parent.ID).WithDetail("from factory").BuildCreate()
	})
	var initial models.Label
	t.Run("create", func(t *testing.T) {
		value, created, err := lookup(root, "once").GetOrCreate(ctx, input)
		check(t, err)
		initial = value
		compare(t, fixture.Creation["create"], map[string]any{"created": created, "detail": value.Detail, "calls": calls, "transaction_commands": trace.take()})
	})
	t.Run("existing", func(t *testing.T) {
		calls = []map[string]bool{}
		value, created, err := lookup(root, "once").GetOrCreate(ctx, singleCreate[models.Label](func() orm.Mutation[models.Label] {
			calls = append(calls, map[string]bool{"in_atomic_block": trace.depth > 0})
			return orm.InvalidMutation[models.Label](&query.Error{Category: query.CategoryField, Code: query.CodeUnknownField})
		}))
		check(t, err)
		compare(t, fixture.Creation["existing"], map[string]any{"created": created, "same_key": value.ID == initial.ID, "detail": value.Detail, "calls": calls, "transaction_commands": trace.take()})
	})
	t.Run("invalid_create_default", func(t *testing.T) {
		generated := models.NewLabelCreate("invalid", parent.ID).BuildCreate()
		assignments := append(generated.Assignments(), query.NewAssignment(query.NewFieldRef("unknown_field", "unknown_field", query.FieldString, false), query.String("invalid for creation")))
		input := singleCreate[models.Label](func() orm.Mutation[models.Label] {
			return orm.NewCreateMutation(models.Label{Name: "invalid", CategoryID: parent.ID}, generated.Table(), assignments)
		})
		_, _, err := lookup(root, "invalid").GetOrCreate(ctx, input)
		compare(t, fixture.Creation["invalid_create_default"], map[string]any{"error": referenceError(err), "exists": exists("invalid")})
		trace.take()
	})
	_, err = models.LabelObjects.Create(ctx, root, models.NewLabelCreate("unique-owner", parent.ID).WithExternal("reserved"))
	check(t, err)
	t.Run("unrelated_unique_conflict", func(t *testing.T) {
		var failed error
		var commands []string
		check(t, root.Atomic(ctx, func(session db.Session) error {
			if _, err := models.LabelObjects.Create(ctx, session, models.NewLabelCreate("parent-before", parent.ID)); err != nil {
				return err
			}
			trace.take()
			_, _, failed = lookup(session, "unrelated-conflict").GetOrCreate(ctx, models.NewLabelCreate("unrelated-conflict", parent.ID).WithExternal("reserved"))
			commands = trace.take()
			_, err := models.LabelObjects.Create(ctx, session, models.NewLabelCreate("parent-after", parent.ID))
			return err
		}))
		trace.take()
		count, err := models.LabelObjects.Using(root).Filter(models.LabelFields.Name.In("parent-before", "parent-after")).Count(ctx)
		check(t, err)
		compare(t, fixture.Creation["unrelated_unique_conflict"], map[string]any{"error": referenceError(failed), "absent": !exists("unrelated-conflict"), "parent_preserved": count == 2, "transaction_commands": commands})
	})
	t.Run("nested_unique_failure", func(t *testing.T) {
		var commands []string
		check(t, root.Atomic(ctx, func(session db.Session) error {
			if _, err := models.LabelObjects.Create(ctx, session, models.NewLabelCreate("outer-before", parent.ID)); err != nil {
				return err
			}
			trace.take()
			err := db.WithSavepoint(ctx, session, func(child db.Session) error {
				if _, err := models.LabelObjects.Create(ctx, child, models.NewLabelCreate("inner-reverted", parent.ID)); err != nil {
					return err
				}
				_, err := models.LabelObjects.Create(ctx, child, models.NewLabelCreate("unique-owner", parent.ID))
				return err
			})
			if referenceError(err) != "IntegrityError" {
				return fmt.Errorf("nested unique failure: %v", err)
			}
			commands = trace.take()
			_, err = models.LabelObjects.Create(ctx, session, models.NewLabelCreate("outer-after", parent.ID))
			return err
		}))
		trace.take()
		count, err := models.LabelObjects.Using(root).Filter(models.LabelFields.Name.In("outer-before", "outer-after")).Count(ctx)
		check(t, err)
		compare(t, fixture.Creation["nested_unique_failure"], map[string]any{"inner_absent": !exists("inner-reverted"), "outer_count": count, "transaction_commands": commands})
	})
	t.Run("release_is_not_commit", func(t *testing.T) {
		abort := errors.New("parent rollback")
		err := root.Atomic(ctx, func(session db.Session) error {
			if err := db.WithSavepoint(ctx, session, func(child db.Session) error {
				_, err := models.LabelObjects.Create(ctx, child, models.NewLabelCreate("inner-released", parent.ID))
				return err
			}); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatal(err)
		}
		compare(t, fixture.Creation["release_is_not_commit"], map[string]any{"absent": !exists("inner-released"), "transaction_commands": trace.take()})
	})
	other, err := models.CategoryObjects.Create(ctx, root, models.NewCategoryCreate("other"))
	check(t, err)
	_, err = models.LabelObjects.Create(ctx, root, models.NewLabelCreate("once", other.ID))
	check(t, err)
	t.Run("multiple_matches", func(t *testing.T) {
		calls = []map[string]bool{}
		_, _, err := models.LabelObjects.Using(root).Filter(models.LabelFields.Name.Exact("once")).GetOrCreate(ctx, input)
		compare(t, fixture.Creation["multiple_matches"], map[string]any{"error": referenceError(err), "factory_calls": len(calls)})
	})
	t.Run("create_outside_filter", func(t *testing.T) {
		original := lookup(root, "outside-filter").Filter(models.LabelFields.Detail.Exact("unmatched-query"))
		value, created, err := original.GetOrCreate(ctx, models.NewLabelCreate("outside-filter", parent.ID).WithDetail("actual"))
		check(t, err)
		count, err := original.Filter(models.LabelFields.ID.Exact(value.ID)).Count(ctx)
		check(t, err)
		compare(t, fixture.Creation["create_outside_filter"], map[string]any{"created": created, "actual_detail": value.Detail, "in_original_filter": count != 0, "transaction_commands": trace.take()})
	})
	t.Run("final_rows", func(t *testing.T) {
		values, err := models.LabelObjects.Using(root).All(ctx)
		check(t, err)
		names := map[int64]string{parent.ID: "support", other.ID: "other"}
		slices.SortFunc(values, func(a, b models.Label) int {
			if cmp := strings.Compare(a.Name, b.Name); cmp != 0 {
				return cmp
			}
			return strings.Compare(names[a.CategoryID], names[b.CategoryID])
		})
		rows := make([]map[string]any, len(values))
		for index, value := range values {
			rows[index] = map[string]any{"name": value.Name, "category__name": names[value.CategoryID], "detail": value.Detail, "external": value.External}
		}
		compare(t, fixture.Creation["final_rows"], map[string]any{"rows": rows})
	})
}
