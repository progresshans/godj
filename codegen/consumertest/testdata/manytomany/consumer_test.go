package consumer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type collectionBackend interface {
	db.Session
	db.RelationAtomic
	db.CoordinatedRelationAtomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

func must[V any](t *testing.T, value V, err error) V {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestCollections(t *testing.T) { withCollectionBackends(t, runCollections) }

func withCollectionBackends(t *testing.T, run func(*testing.T, collectionBackend, func() (collectionBackend, error), func(string) error, bool)) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "collections.sqlite3")) + "?mode=rwc&_pragma=busy_timeout(10000)"
		first, err := sqlite.Open(t.Context(), dsn)
		check(t, err)
		run(t, first, func() (collectionBackend, error) { return sqlite.Open(t.Context(), dsn) }, func(s string) error { _, err := first.ExecContext(t.Context(), s); return err }, false)
	})
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL is absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), url)
		check(t, err)
		schema := fmt.Sprintf("godj_collections_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		_, err = connection.Exec(t.Context(), "CREATE SCHEMA "+quoted)
		check(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			if err != nil {
				t.Error(err)
			}
			if err := connection.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		_, err = connection.Exec(t.Context(), "SET search_path TO "+quoted)
		check(t, err)
		open := func() (collectionBackend, error) {
			return postgres.Open(t.Context(), postgres.Config{URL: url, Schema: schema})
		}
		first, err := open()
		check(t, err)
		run(t, first, open, func(s string) error { _, err := connection.Exec(t.Context(), s); return err }, true)
	})
}

func migrateCollections(t *testing.T, b collectionBackend) {
	target := labels.GoDjRelationSchema()
	source := owners.GoDjRelationSchema()
	targetMigration := migrations.Migration{App: "labels", Name: "0001_initial"}
	for _, model := range target.Models {
		targetMigration.Operations = append(targetMigration.Operations, migrations.CreateModel{AppLabel: "labels", Model: model})
	}
	migrationsList := []migrations.Migration{targetMigration}
	migration := migrations.Migration{App: "owners", Name: "0001_initial", Dependencies: []migrations.MigrationKey{{App: "labels", Name: "0001_initial"}}}
	var many []ir.ManyToManyField
	for _, model := range source.Models {
		if model.Name == "owner" {
			many = model.ManyToMany
			model.ManyToMany = nil
		}
		migration.Operations = append(migration.Operations, migrations.CreateModel{AppLabel: "owners", Model: model})
	}
	for _, field := range many {
		migration.Operations = append(migration.Operations, migrations.AddManyToMany{AppLabel: "owners", ModelName: "owner", Field: field})
	}
	// Policy models are declared after their through dependency. Normalization
	// preserves this authored order; the migration exercises actual storage.
	migrationsList = append(migrationsList, migration)
	var sources []definition.Source
	for _, migration := range migrationsList {
		wire, err := definition.Encode(definition.Producer{Name: "many-collection-consumer", Version: "1"}, migration)
		check(t, err)
		sources = append(sources, definition.Source{SourceID: migration.App, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	check(t, err)
	_, err = (migrations.Executor{Backend: b}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	check(t, err)
}

func runCollections(t *testing.T, b collectionBackend, open func() (collectionBackend, error), exec func(string) error, pg bool) {
	ctx := t.Context()
	t.Cleanup(func() {
		if b != nil {
			if err := b.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	migrateCollections(t, b)
	collections, err := project.BindCollections()
	check(t, err)
	owner, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("first"))
	check(t, err)
	other, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate("second"))
	check(t, err)
	var targets []labels.Label
	for _, name := range []string{"a", "b", "c"} {
		label, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate(name))
		check(t, err)
		targets = append(targets, label)
	}
	a, bb, cc := targets[0], targets[1], targets[2]
	links, err := collections.OwnersOwnerLabels.From(b, owner)
	check(t, err)
	get := func(c *orm.ManyCollection[labels.Label, owners.OwnerLabelsLink]) []string {
		rows, err := c.All(ctx)
		check(t, err)
		names := []string{}
		for _, row := range rows {
			names = append(names, row.Name)
		}
		slices.Sort(names)
		return names
	}
	assertNames := func(c *orm.ManyCollection[labels.Label, owners.OwnerLabelsLink], want ...string) {
		t.Helper()
		if got := get(c); !slices.Equal(got, want) {
			t.Fatalf("labels %v want %v", got, want)
		}
	}
	t.Run("add_reverse_cache_presence", func(t *testing.T) {
		check(t, links.Add(ctx, []labels.Label{a, a, bb}))
		assertNames(links, "a", "b")
		held, err := links.Query()
		check(t, err)
		_, err = held.All(ctx)
		check(t, err)
		independent, err := collections.OwnersOwnerLabels.From(b, owner)
		check(t, err)
		assertNames(independent, "a", "b")
		check(t, links.AddKeys(ctx, []int64{cc.ID}))
		assertNames(links, "a", "b", "c")
		assertNames(independent, "a", "b")
		old, err := held.All(ctx)
		check(t, err)
		if len(old) != 2 {
			t.Fatal("held query cache changed")
		}
		fresh, err := independent.Fresh()
		check(t, err)
		assertNames(fresh, "a", "b", "c")
		if err := independent.Add(ctx, []labels.Label{labels.Label{ID: a.ID}}); !errors.Is(err, &query.Error{Code: query.CodeMissingPrimaryKey}) {
			t.Fatal(err)
		}
		assertNames(independent, "a", "b", "c")

		reverse, err := collections.LabelsLabelOwners.From(b, a)
		check(t, err)
		check(t, reverse.Add(ctx, []owners.Owner{other}))
		rows, err := reverse.All(ctx)
		check(t, err)
		if len(rows) != 2 {
			t.Fatal(rows)
		}
		if err := links.Add(ctx, []labels.Label{labels.Label{ID: a.ID}}); !errors.Is(err, &query.Error{Code: query.CodeMissingPrimaryKey}) {
			t.Fatal("unsaved key accepted", err)
		}
		copied := *links
		if _, err := copied.All(ctx); err == nil {
			t.Fatal("copied handle accepted")
		}
		if _, err := collections.OwnersOwnerLabels.From(b, owners.Owner{ID: owner.ID}); err == nil {
			t.Fatal("unsaved owner accepted")
		}
		check(t, exec("INSERT INTO labels_label (id,name) VALUES (0,'zero')"))
		check(t, links.Add(ctx, []labels.Label{labels.NewLabelWithID(0)}))
		assertNames(links, "a", "b", "c", "zero")
		check(t, links.RemoveKeys(ctx, 0, 99999))
		assertNames(links, "a", "b", "c")
		check(t, exec("INSERT INTO owners_owner (id,name) VALUES (0,'zero-owner')"))
		zero, err := collections.OwnersOwnerLabels.From(b, owners.NewOwnerWithID(0))
		check(t, err)
		check(t, zero.AddKeys(ctx, []int64{a.ID}))
		check(t, zero.Clear(ctx))
	})
	t.Run("retained_set_clear_failure_rollback", func(t *testing.T) {
		before, err := owners.OwnerLabelsLinkObjects.Using(b).OrderBy(owners.OwnerLabelsLinkFields.ID.Asc()).All(ctx)
		check(t, err)
		check(t, links.Set(ctx, []labels.Label{bb, cc}))
		assertNames(links, "b", "c")
		after, err := owners.OwnerLabelsLinkObjects.Using(b).All(ctx)
		check(t, err)
		for _, row := range after {
			if row.SourceID == owner.ID {
				found := false
				for _, old := range before {
					if old.ID == row.ID && old.TargetID == row.TargetID {
						found = true
					}
				}
				if !found {
					t.Fatal("retained identity replaced")
				}
			}
		}
		if err := links.SetKeys(ctx, []int64{a.ID, 99999}); err == nil {
			t.Fatal("missing target accepted")
		}
		assertNames(links, "b", "c")
		check(t, links.Set(ctx, []labels.Label{bb, cc}, orm.ManyToManySetOptions[owners.OwnerLabelsLink]{Clear: true}))
		replaced, err := owners.OwnerLabelsLinkObjects.Using(b).All(ctx)
		check(t, err)
		for _, row := range replaced {
			if row.SourceID == owner.ID {
				for _, old := range after {
					if row.ID == old.ID {
						t.Fatal("clear set retained link")
					}
				}
			}
		}
		check(t, links.Clear(ctx))
		assertNames(links)
		reverse, err := collections.LabelsLabelOwners.From(b, a)
		check(t, err)
		remaining, err := reverse.All(ctx)
		check(t, err)
		if len(remaining) != 1 || remaining[0].ID != other.ID {
			t.Fatal("clear affected another owner")
		}
		count, err := labels.LabelObjects.Using(b).Count(ctx)
		check(t, err)
		if count != 4 {
			t.Fatal("clear deleted endpoints", count)
		}
	})
	t.Run("nullable_payload_and_incoming_policy", func(t *testing.T) {
		ranked, err := collections.OwnersOwnerRanked.From(b, owner)
		check(t, err)
		check(t, ranked.Add(ctx, []labels.Label{a}, owners.RankedLinkCreate{}.WithAmount(17)))
		original, err := owners.RankedLinkObjects.Using(b).All(ctx)
		check(t, err)
		if len(original) != 1 {
			t.Fatal(original)
		}
		originalID := original[0].ID
		// Missing required defaults on a no-op must not change retained payload.
		check(t, ranked.Add(ctx, []labels.Label{a}))
		if err := ranked.Set(ctx, []labels.Label{bb, cc}, orm.ManyToManySetOptions[owners.RankedLink]{ThroughDefaults: owners.RankedLinkCreate{}.WithAmount(21)}); err == nil {
			t.Fatal("late payload uniqueness failure accepted")
		}
		unchanged, err := owners.RankedLinkObjects.Using(b).All(ctx)
		check(t, err)
		if !reflect.DeepEqual(original, unchanged) {
			t.Fatal("payload failure lost retained link", unchanged)
		}
		if err := ranked.Add(ctx, []labels.Label{bb}, owners.RankedLinkCreate{}.WithOwnerID(other.ID).WithAmount(22)); err == nil {
			t.Fatal("through defaults redirected owner")
		}
		check(t, ranked.Add(ctx, []labels.Label{bb}, owners.RankedLinkCreate{}.WithAmount(22)))
		two, err := owners.RankedLinkObjects.Using(b).OrderBy(owners.RankedLinkFields.ID.Asc()).All(ctx)
		check(t, err)
		guard, err := owners.ProtectedObjects.Create(ctx, b, owners.NewProtectedCreate(two[1].ID))
		check(t, err)
		if err := ranked.Clear(ctx); !errors.Is(err, &query.Error{Code: query.CodeProtectedForeignKey}) {
			t.Fatal("PROTECT lost", err)
		}
		retained, err := owners.RankedLinkObjects.Using(b).All(ctx)
		check(t, err)
		if len(retained) != 2 {
			t.Fatal("earlier root deleted before PROTECT")
		}
		_, err = owners.ProtectedObjects.Delete(ctx, b, &guard)
		check(t, err)
		_, err = owners.CascadeObjects.Create(ctx, b, owners.NewCascadeCreate(originalID))
		check(t, err)
		optional, err := owners.OptionalObjects.Create(ctx, b, owners.NewOptionalCreate().WithLinkID(originalID))
		check(t, err)
		check(t, ranked.Remove(ctx, a))
		count, err := owners.CascadeObjects.Using(b).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("incoming CASCADE ignored")
		}
		options, err := owners.OptionalObjects.Using(b).All(ctx)
		check(t, err)
		if len(options) != 1 || options[0].ID != optional.ID || options[0].LinkID != nil {
			t.Fatal("incoming SET_NULL ignored")
		}
		nullLink, err := owners.RankedLinkObjects.Create(ctx, b, owners.NewRankedLinkCreate(23).WithOwnerID(owner.ID))
		check(t, err)
		check(t, ranked.Set(ctx, []labels.Label{bb}))
		nulls, err := owners.RankedLinkObjects.Using(b).All(ctx)
		check(t, err)
		if len(nulls) != 2 {
			t.Fatal("set changed NULL non-membership", nullLink.ID)
		}
		check(t, ranked.Clear(ctx))
		count, err = owners.RankedLinkObjects.Using(b).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("clear left NULL link")
		}
	})
	t.Run("nonunique_multiplicity", func(t *testing.T) {
		loose, err := collections.OwnersOwnerLoose.From(b, owner)
		check(t, err)
		for _, amount := range []int64{4, 5} {
			_, err := owners.LooseLinkObjects.Create(ctx, b, owners.NewLooseLinkCreate(amount).WithOwnerID(owner.ID).WithLabelID(a.ID))
			check(t, err)
		}
		rows, err := loose.All(ctx)
		check(t, err)
		if len(rows) != 2 {
			t.Fatal("multiplicity collapsed", len(rows))
		}
		set, err := loose.Query()
		check(t, err)
		rows, err = set.Distinct().All(ctx)
		check(t, err)
		if len(rows) != 1 {
			t.Fatal("explicit distinct failed")
		}
		check(t, loose.Add(ctx, []labels.Label{a}, owners.LooseLinkCreate{}.WithAmount(9)))
		count, err := owners.LooseLinkObjects.Using(b).Count(ctx)
		check(t, err)
		if count != 2 {
			t.Fatal("duplicate add changed nonunique links")
		}
		check(t, loose.Set(ctx, []labels.Label{a}))
		check(t, loose.Remove(ctx, a))
		count, err = owners.LooseLinkObjects.Using(b).Count(ctx)
		check(t, err)
		if count != 0 {
			t.Fatal("remove failed to delete all duplicates")
		}
	})
	t.Run("symmetry_and_directed_reverse", func(t *testing.T) {
		friends, err := collections.OwnersOwnerFriends.From(b, owner)
		check(t, err)
		check(t, friends.Add(ctx, []owners.Owner{owner, other, other}))
		rows, err := owners.OwnerFriendsLinkObjects.Using(b).All(ctx)
		check(t, err)
		if len(rows) != 3 {
			t.Fatal("self or mirror count", len(rows))
		}
		otherFriends, err := collections.OwnersOwnerFriends.From(b, other)
		check(t, err)
		values, err := otherFriends.All(ctx)
		check(t, err)
		if len(values) != 1 || values[0].ID != owner.ID {
			t.Fatal("mirror missing")
		}
		check(t, otherFriends.Clear(ctx))
		fresh, err := friends.Fresh()
		check(t, err)
		values, err = fresh.All(ctx)
		check(t, err)
		if len(values) != 1 || values[0].ID != owner.ID {
			t.Fatal("clear removed wrong self links")
		}
		if err := friends.SetKeys(ctx, []int64{other.ID, 99999}); err == nil {
			t.Fatal("symmetrical partial failure accepted")
		}
		unchanged, err := owners.OwnerFriendsLinkObjects.Using(b).All(ctx)
		check(t, err)
		if len(unchanged) != 1 || unchanged[0].SourceID != owner.ID || unchanged[0].TargetID != owner.ID {
			t.Fatal("symmetrical rollback lost links")
		}
		follows, err := collections.OwnersOwnerFollows.From(b, owner)
		check(t, err)
		check(t, follows.Add(ctx, []owners.Owner{other}))
		following, err := collections.OwnersOwnerFollows.From(b, other)
		check(t, err)
		values, err = following.All(ctx)
		check(t, err)
		if len(values) != 0 {
			t.Fatal("directed relation mirrored")
		}
		followers, err := collections.OwnersOwnerFollowers.From(b, other)
		check(t, err)
		check(t, followers.Clear(ctx))
		freshFollow, err := follows.Fresh()
		check(t, err)
		values, err = freshFollow.All(ctx)
		check(t, err)
		if len(values) != 0 {
			t.Fatal("reverse clear ignored")
		}
	})
	t.Run("concurrent_duplicate_and_cache", func(t *testing.T) {
		second, err := open()
		check(t, err)
		defer func() { check(t, second.Close()) }()
		otherLinks, err := collections.OwnersOwnerLabels.From(second, owner)
		check(t, err)
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, c := range []*orm.ManyCollection[labels.Label, owners.OwnerLabelsLink]{links, otherLinks} {
			wg.Go(func() { <-start; results <- c.Add(ctx, []labels.Label{a, a}) })
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			check(t, err)
		}
		fresh, err := links.Fresh()
		check(t, err)
		assertNames(fresh, "a")
		count, err := owners.OwnerLabelsLinkObjects.Using(b).Count(ctx)
		check(t, err)
		if count != 2 {
			t.Fatal("concurrent duplicate storage", count)
		}
		var readers sync.WaitGroup
		for range 8 {
			readers.Go(func() {
				for range 10 {
					_, err := links.All(ctx)
					if err != nil {
						t.Error(err)
					}
				}
			})
		}
		check(t, links.Add(ctx, []labels.Label{bb}))
		readers.Wait()
		assertNames(links, "a", "b")
	})
	t.Run("trigger_noop_and_cancel_rollback", func(t *testing.T) {
		if pg {
			check(t, exec(`CREATE FUNCTION skip_collection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$`))
			check(t, exec(`CREATE TRIGGER skip_collection BEFORE INSERT ON owners_owner_labels FOR EACH ROW EXECUTE FUNCTION skip_collection()`))
		} else {
			check(t, exec(`CREATE TRIGGER skip_collection BEFORE INSERT ON owners_owner_labels BEGIN SELECT RAISE(IGNORE); END`))
		}
		if err := links.Add(ctx, []labels.Label{cc}); err == nil {
			t.Fatal("trigger no-op reported membership")
		}
		assertNames(links, "a", "b")
		check(t, exec(`DROP TRIGGER skip_collection`+func() string {
			if pg {
				return " ON owners_owner_labels"
			}
			return ""
		}()))
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()
		if err := links.Clear(cancelCtx); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
		assertNames(links, "a", "b")
		wrapped := &hookBackend{collectionBackend: b}
		interrupted, cancelInsert := context.WithCancel(ctx)
		wrapped.atomic = func(ctx context.Context, callback func(db.RelationSession) error) error {
			return b.AtomicRelation(ctx, func(session db.RelationSession) error {
				return callback(cancelInsertSession{RelationSession: session, cancel: cancelInsert})
			})
		}
		interruptedLinks, err := collections.OwnersOwnerLabels.From(wrapped, owner)
		check(t, err)
		if err := interruptedLinks.Set(interrupted, []labels.Label{cc}); !errors.Is(err, context.Canceled) {
			t.Fatal("insertion cancellation lost", err)
		}
		cancelInsert()
		assertNames(interruptedLinks, "a", "b")

		wrapped.atomic = func(ctx context.Context, callback func(db.RelationSession) error) error {
			return b.AtomicRelation(ctx, func(s db.RelationSession) error {
				err := callback(s)
				if err != nil {
					return err
				}
				return context.Canceled
			})
		}
		failed, err := collections.OwnersOwnerLabels.From(wrapped, owner)
		check(t, err)
		if err := failed.Set(ctx, []labels.Label{cc}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		assertNames(failed, "a", "b")
		wrapped.atomic = func(ctx context.Context, callback func(db.RelationSession) error) error {
			if err := b.AtomicRelation(ctx, callback); err != nil {
				return err
			}
			return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
		}
		if err := failed.Add(ctx, []labels.Label{cc}); !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
			t.Fatal("unknown outcome was published as success", err)
		}
		assertNames(failed, "a", "b", "c")
		committedCtx, cancelAfter := context.WithCancel(ctx)
		defer cancelAfter()
		wrapped.atomic = func(ctx context.Context, callback func(db.RelationSession) error) error {
			if err := b.AtomicRelation(ctx, callback); err != nil {
				return err
			}
			cancelAfter()
			return nil
		}
		check(t, failed.Clear(committedCtx))
		assertNames(failed)
	})
	check(t, b.Close())
	b = nil
	reopened, err := open()
	check(t, err)
	defer func() { check(t, reopened.Close()) }()
	final, err := collections.OwnersOwnerLabels.From(reopened, owner)
	check(t, err)
	assertNames(final)
}

type hookBackend struct {
	collectionBackend
	atomic func(context.Context, func(db.RelationSession) error) error
}

func (b *hookBackend) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if b.atomic != nil {
		return b.atomic(ctx, callback)
	}
	return b.collectionBackend.AtomicRelation(ctx, callback)
}
func (b *hookBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if b.collectionBackend != nil {
		return b.collectionBackend.Query(ctx, plan)
	}
	return nil, errors.New("unexpected root query")
}

func TestCollectionBindingAndCallbackOwnership(t *testing.T) {
	collections, err := project.BindCollections()
	check(t, err)
	if _, err := collections.OwnersOwnerLabels.From(nil, owners.NewOwnerWithID(1)); err == nil {
		t.Fatal("nil backend accepted")
	}
	var zero orm.ManyToMany[owners.Owner, labels.Label, owners.OwnerLabelsLink]
	b := &hookBackend{}
	if _, err := zero.From(b, owners.NewOwnerWithID(1)); err == nil {
		t.Fatal("unbound relation accepted")
	}
	for _, mode := range []string{"zero", "twice", "swallow", "late"} {
		t.Run(mode, func(t *testing.T) {
			var retained func(db.RelationSession) error
			b := &hookBackend{atomic: func(ctx context.Context, callback func(db.RelationSession) error) error {
				retained = callback
				if mode == "zero" || mode == "late" {
					return nil
				}
				first := callback(nil)
				if mode == "twice" {
					_ = callback(nil)
					return first
				}
				return nil
			}}
			c, err := collections.OwnersOwnerLabels.From(b, owners.NewOwnerWithID(1))
			check(t, err)
			if err := c.Clear(t.Context()); err == nil {
				t.Fatal("callback contract failed open")
			}
			if mode == "late" {
				if err := retained(nil); err == nil {
					t.Fatal("late callback accepted")
				}
			}
		})
	}
	// Runtime binders seal the actual declaration and every incoming link policy.
	binding, err := project.Bind()
	check(t, err)
	source, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}, owners.OwnerDescriptor{})
	check(t, err)
	target, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}, labels.LabelDescriptor{})
	check(t, err)
	link, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "owners", ModelName: "ranked_link"}, owners.RankedLinkDescriptor{})
	check(t, err)
	if _, err := orm.BindManyToMany(source, "ranked", target, link, strings.Repeat("0", 64)); err == nil {
		t.Fatal("stale delete fingerprint accepted")
	}
	other, err := project.Bind()
	check(t, err)
	separate, err := orm.BindModel(other, ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}, labels.LabelDescriptor{})
	check(t, err)
	if _, err := orm.BindManyToMany(source, "ranked", separate, link, strings.Repeat("0", 64)); err == nil {
		t.Fatal("mixed snapshot accepted")
	}
}

type cancelInsertSession struct {
	db.RelationSession
	cancel context.CancelFunc
}

func (s cancelInsertSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	inserted, err := s.RelationSession.(db.ConflictInserter).InsertOnConflict(ctx, plan)
	s.cancel()
	return inserted, err
}
