package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	fixture "github.com/progresshans/godj/admin/testdata/choicereferencefixture"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"golang.org/x/net/html"
)

type adminChoiceDatabase interface {
	db.Queryer
	db.Mutator
	db.RelationAtomic
	Close() error
}
type adminChoiceRow struct {
	Source fixture.Source
	Many   []int64
}
type adminChoiceDomain struct {
	Targets []adminChoiceTarget `json:"targets"`
	Source  adminChoiceSource   `json:"source"`
}
type adminChoiceNative struct {
	models   fixture.Fixture
	database adminChoiceDatabase
	cleanup  func() error
	backend  string
	profile  map[string]string
}

func adminChoiceInt(t *testing.T, value string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != value {
		t.Fatalf("noncanonical fixture integer %q", value)
	}
	return n
}
func adminChoiceText(value int64) string { return strconv.FormatInt(value, 10) }
func adminChoiceKeysText(keys []int64) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = adminChoiceText(key)
	}
	return out
}

func adminChoiceOpen(t *testing.T, backend string, inputs adminChoiceInputs) *adminChoiceNative {
	t.Helper()
	models, err := fixture.New()
	if err != nil {
		t.Fatal(err)
	}
	native := &adminChoiceNative{models: models, backend: backend, profile: map[string]string{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "backend": backend}}
	var exec func(string, ...any) error
	var table func(string) string
	var cleanup func() error
	if backend == "sqlite" {
		path := filepath.Join(t.TempDir(), "admin-choice.sqlite3")
		database, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		native.database = database
		version, err := database.SQLiteVersion(t.Context())
		if err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		native.profile["database_version"] = version
		exec = func(sql string, args ...any) error {
			_, err := database.ExecContext(t.Context(), sql, args...)
			return err
		}
		table = func(name string) string { return pgx.Identifier{name}.Sanitize() }
		cleanup = func() error {
			if err := database.Close(); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				return errors.New("owned SQLite file remains")
			}
			return nil
		}
	} else {
		dsn := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
		if dsn == "" {
			t.Fatal("required native PostgreSQL endpoint absent")
		}
		control, err := pgx.Connect(t.Context(), dsn)
		if err != nil {
			t.Fatal("connect Admin choice PostgreSQL")
		}
		var version string
		if err := control.QueryRow(t.Context(), "SHOW server_version").Scan(&version); err != nil {
			_ = control.Close(context.Background())
			t.Fatal(err)
		}
		native.profile["database_version"] = version
		name := fmt.Sprintf("godj_admin_choice_native_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err := control.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
			_ = control.Close(context.Background())
			t.Fatal(err)
		}
		// Ownership starts only after CREATE succeeded. The independent Django
		// observer owns a different schema and never sees these Go tables.
		cleanup = func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var failures []error
			if native.database != nil {
				failures = append(failures, native.database.Close())
			}
			_, dropErr := control.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			failures = append(failures, dropErr)
			var exists bool
			checkErr := control.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", name).Scan(&exists)
			failures = append(failures, checkErr)
			if exists {
				failures = append(failures, errors.New("owned PostgreSQL schema remains"))
			}
			failures = append(failures, control.Close(ctx))
			return errors.Join(failures...)
		}
		database, err := postgres.Open(t.Context(), postgres.Config{URL: dsn, Schema: name})
		if err != nil {
			_ = cleanup()
			t.Fatal(err)
		}
		native.database = database
		exec = func(sql string, args ...any) error { _, err := control.Exec(t.Context(), sql, args...); return err }
		table = func(table string) string { return pgx.Identifier{name, table}.Sanitize() }
	}
	closed := false
	native.cleanup = func() error {
		if closed {
			return nil
		}
		closed = true
		return cleanup()
	}
	t.Cleanup(func() {
		if err := native.cleanup(); err != nil {
			t.Error(err)
		}
	})
	target, source, link := table("gdj_admin_choice_target"), table("gdj_admin_choice_source"), table("gdj_admin_choice_link")
	auto := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if backend == "postgres" {
		auto = "BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY"
	}
	statements := []string{
		"CREATE TABLE " + target + " (id BIGINT PRIMARY KEY, tenant BIGINT NOT NULL, label VARCHAR(120) NOT NULL, enabled BOOLEAN NOT NULL)",
		"CREATE TABLE " + source + " (id BIGINT PRIMARY KEY, tenant BIGINT NOT NULL, foreign_key_id BIGINT NOT NULL REFERENCES " + target + "(id), one_to_one_id BIGINT NOT NULL UNIQUE REFERENCES " + target + "(id))",
		"CREATE TABLE " + link + " (id " + auto + ", source_id BIGINT NOT NULL REFERENCES " + source + "(id) ON DELETE CASCADE, target_id BIGINT NOT NULL REFERENCES " + target + "(id) ON DELETE CASCADE, UNIQUE(source_id,target_id))",
	}
	for _, sql := range statements {
		if err := exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range inputs.Targets {
		if err := exec("INSERT INTO "+target+"(id,tenant,label,enabled) VALUES($1,$2,$3,$4)", adminChoiceInt(t, row.Key), adminChoiceInt(t, row.Tenant), row.Label, row.Enabled); err != nil {
			t.Fatal(err)
		}
	}
	seed := inputs.Source
	if err := exec("INSERT INTO "+source+"(id,tenant,foreign_key_id,one_to_one_id) VALUES($1,$2,$3,$4)", adminChoiceInt(t, seed.Key), adminChoiceInt(t, seed.Tenant), adminChoiceInt(t, seed.Foreign), adminChoiceInt(t, seed.Single)); err != nil {
		t.Fatal(err)
	}
	for _, key := range seed.Many {
		if err := exec("INSERT INTO "+link+"(source_id,target_id) VALUES($1,$2)", adminChoiceInt(t, seed.Key), adminChoiceInt(t, key)); err != nil {
			t.Fatal(err)
		}
	}
	return native
}

func (native *adminChoiceNative) row(ctx context.Context, reader db.Queryer) (adminChoiceRow, error) {
	model := native.models
	source, found, err := model.Sources.Using(reader).Filter(model.SourceID.Exact(1)).OrderBy(model.SourceID.Asc()).First(ctx)
	if err != nil {
		return adminChoiceRow{}, err
	}
	if !found {
		return adminChoiceRow{}, ErrObjectNotFound
	}
	links, err := model.Links.Using(reader).Filter(model.LinkSource.Exact(source.ID)).OrderBy(model.LinkTarget.Asc()).All(ctx)
	if err != nil {
		return adminChoiceRow{}, err
	}
	keys := make([]int64, len(links))
	for i, link := range links {
		keys[i] = link.TargetID
	}
	return adminChoiceRow{source, keys}, nil
}
func adminChoiceStored(row adminChoiceRow) adminChoiceSource {
	return adminChoiceSource{adminChoiceText(row.Source.ID), adminChoiceText(row.Source.Tenant), adminChoiceText(row.Source.ForeignKey), adminChoiceText(row.Source.OneToOne), adminChoiceKeysText(row.Many)}
}
func (native *adminChoiceNative) domain(ctx context.Context, reader db.Queryer) (adminChoiceDomain, error) {
	row, err := native.row(ctx, reader)
	if err != nil {
		return adminChoiceDomain{}, err
	}
	targets, err := native.models.Targets.Using(reader).OrderBy(native.models.TargetID.Asc()).All(ctx)
	if err != nil {
		return adminChoiceDomain{}, err
	}
	out := adminChoiceDomain{Targets: make([]adminChoiceTarget, len(targets)), Source: adminChoiceStored(row)}
	for i, target := range targets {
		out.Targets[i] = adminChoiceTarget{adminChoiceText(target.ID), adminChoiceText(target.Tenant), target.Label, target.Enabled}
	}
	return out, nil
}
func (native *adminChoiceNative) revoke(ctx context.Context, session db.Session, keys []string) error {
	for _, text := range keys {
		key, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return err
		}
		row, found, err := native.models.Targets.Using(session).Filter(native.models.TargetID.Exact(key)).OrderBy(native.models.TargetID.Asc()).First(ctx)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("revocation fixture target absent")
		}
		row.Enabled = false
		if err := native.models.Targets.Save(ctx, session, &row, orm.ForceUpdate[fixture.Target]()); err != nil {
			return err
		}
	}
	return nil
}
func (native *adminChoiceNative) collectionSaver() formmodel.CollectionSaver[fixture.Source] {
	// This explicit application saver is intentionally independent of Django's
	// automatic through model. It exercises PreparedInstance's borrowed-session
	// contract with typed Go reads/deletes/inserts and an identity-generated link.
	return formmodel.CollectionSaver[fixture.Source]{Field: "many", Save: func(ctx context.Context, session db.Session, owner fixture.Source, keys []int64) error {
		links, err := native.models.Links.Using(session).Filter(native.models.LinkSource.Exact(owner.ID)).OrderBy(native.models.LinkID.Asc()).All(ctx)
		if err != nil {
			return err
		}
		for _, link := range links {
			if _, err := native.models.Links.Delete(ctx, session, &link); err != nil {
				return err
			}
		}
		for _, key := range keys {
			link := fixture.Link{SourceID: owner.ID, TargetID: key}
			if err := native.models.Links.Save(ctx, session, &link, orm.ForceInsert[fixture.Link]()); err != nil {
				return err
			}
		}
		return nil
	}}
}
func (native *adminChoiceNative) restore(ctx context.Context, original adminChoiceDomain) error {
	return native.database.AtomicRelation(ctx, func(session db.RelationSession) error {
		for _, target := range original.Targets {
			id, _ := strconv.ParseInt(target.Key, 10, 64)
			tenant, _ := strconv.ParseInt(target.Tenant, 10, 64)
			value := fixture.NewTarget(id, tenant, target.Label, target.Enabled)
			if err := native.models.Targets.Save(ctx, session, &value, orm.ForceUpdate[fixture.Target]()); err != nil {
				return err
			}
		}
		row := original.Source
		id, _ := strconv.ParseInt(row.Key, 10, 64)
		tenant, _ := strconv.ParseInt(row.Tenant, 10, 64)
		foreign, _ := strconv.ParseInt(row.Foreign, 10, 64)
		single, _ := strconv.ParseInt(row.Single, 10, 64)
		value := fixture.NewSource(id, tenant, foreign, single)
		if err := native.models.Sources.Save(ctx, session, &value, orm.ForceUpdate[fixture.Source]()); err != nil {
			return err
		}
		keys := make([]int64, len(row.Many))
		for i, text := range row.Many {
			keys[i], _ = strconv.ParseInt(text, 10, 64)
		}
		return native.collectionSaver().Save(ctx, session, value, keys)
	})
}
func (native *adminChoiceNative) choices(relation string) (formmodel.ModelChoices[fixture.Target], error) {
	m := native.models
	return formmodel.NewRelationChoices(m.Project, ir.ModelIdentity{AppLabel: fixture.App, ModelName: "source"}, relation, m.Targets, formmodel.ModelChoiceConfig[fixture.Target]{
		Label: func(target fixture.Target) (string, error) { return target.Label, nil },
		Order: []orm.Ordering[fixture.Target]{m.Label.Asc(), m.TargetID.Asc()},
		Search: func(term string) ([]orm.Predicate[fixture.Target], error) {
			return []orm.Predicate[fixture.Target]{m.Label.IContains(term)}, nil
		},
	})
}
func (native *adminChoiceNative) predicates(actor adminChoiceActor) ([]orm.Predicate[fixture.Target], error) {
	tenant, err := strconv.ParseInt(actor.Tenant, 10, 64)
	if err != nil {
		return nil, err
	}
	return []orm.Predicate[fixture.Target]{native.models.Tenant.Exact(tenant), native.models.Enabled.Exact(true)}, nil
}
func adminChoiceInitial(row adminChoiceRow) map[string]forms.Value {
	return map[string]forms.Value{"id": forms.Integer(row.Source.ID), "tenant": forms.Integer(row.Source.Tenant), "foreign_key": forms.Integer(row.Source.ForeignKey), "one_to_one": forms.Integer(row.Source.OneToOne), "many": forms.Integers(row.Many...)}
}
func (native *adminChoiceNative) spec(ctx context.Context, reader db.Queryer, actor adminChoiceActor, relation string, data forms.Data, initial map[string]forms.Value, unbound bool) (forms.Spec, error) {
	metadata, err := native.models.Sources.Metadata()
	if err != nil {
		return forms.Spec{}, err
	}
	spec, err := (formmodel.Definition{Fields: []string{relation}}).Spec(metadata)
	if err != nil {
		return forms.Spec{}, err
	}
	input, err := formmodel.PrepareChoiceInput(metadata, spec, data, initial)
	if err != nil {
		return forms.Spec{}, err
	}
	keys, valid, err := input.ChoiceKeys(relation)
	if unbound {
		keys, valid, err = input.InitialChoiceKeys(relation)
	}
	if err != nil {
		return forms.Spec{}, err
	}
	if !valid {
		keys = nil
	}
	choices, err := native.choices(relation)
	if err != nil {
		return forms.Spec{}, err
	}
	predicates, err := native.predicates(actor)
	if err != nil {
		return forms.Spec{}, err
	}
	selection, err := choices.Filter(predicates...).Resolve(ctx, reader, keys)
	if err != nil {
		return forms.Spec{}, err
	}
	return spec.WithModelChoices(relation, selection.Items()...)
}
func (native *adminChoiceNative) bind(ctx context.Context, reader db.Queryer, actor adminChoiceActor, relation string, data forms.Data, row adminChoiceRow) (formmodel.BoundForm, error) {
	initial := adminChoiceInitial(row)
	spec, err := native.spec(ctx, reader, actor, relation, data, initial, false)
	if err != nil {
		return formmodel.BoundForm{}, err
	}
	metadata, err := native.models.Sources.Metadata()
	if err != nil {
		return formmodel.BoundForm{}, err
	}
	return formmodel.Bind(ctx, metadata, spec, data, initial, formmodel.PostClean{})
}
func (native *adminChoiceNative) save(ctx context.Context, session db.Session, relation string, bound formmodel.BoundForm, current adminChoiceRow) error {
	prepared, err := formmodel.PrepareInstance(native.models.Sources, bound, &current.Source)
	if err != nil {
		return err
	}
	value, err := prepared.Model()
	if err != nil {
		return err
	}
	var savers []formmodel.CollectionSaver[fixture.Source]
	if relation == "many" {
		savers = append(savers, native.collectionSaver())
	}
	return prepared.Save(ctx, session, &value, savers...)
}
func adminChoiceFormValues(form forms.Form) (map[string][]string, map[string]json.RawMessage, error) {
	codes, cleaned := map[string][]string{}, map[string]json.RawMessage{}
	for _, failure := range form.Errors().All() {
		name := string(failure.Field())
		codes[name] = append(codes[name], string(failure.Code()))
	}
	for _, name := range []string{"foreign_key", "one_to_one", "many"} {
		value, exists := form.Cleaned().Get(name)
		if !exists {
			continue
		}
		var native any
		if value.IsNull() {
			native = nil
		} else if keys, ok := value.AsIntegers(); ok {
			native = adminChoiceKeysText(keys)
		} else if key, ok := value.AsInteger(); ok {
			native = adminChoiceText(key)
		} else {
			return nil, nil, errors.New("unexpected cleaned relation type")
		}
		raw, err := json.Marshal(native)
		if err != nil {
			return nil, nil, err
		}
		cleaned[name] = raw
	}
	return codes, cleaned, nil
}

type adminChoiceFormProof struct {
	Valid      *bool                      `json:"valid"`
	Codes      map[string][]string        `json:"codes"`
	Cleaned    map[string]json.RawMessage `json:"cleaned"`
	Saved      bool                       `json:"saved"`
	Before     adminChoiceSource          `json:"stored_before"`
	After      adminChoiceSource          `json:"stored_after"`
	RolledBack bool                       `json:"transaction_rolled_back"`
}

func (native *adminChoiceNative) observeForm(t *testing.T, caseInput adminChoiceCase) adminChoiceFormProof {
	t.Helper()
	var input adminChoiceFormInput
	if err := adminChoiceDecode(caseInput.Input, &input); err != nil {
		t.Fatal(err)
	}
	original, err := native.domain(t.Context(), native.database)
	if err != nil {
		t.Fatal(err)
	}
	proof := adminChoiceFormProof{Codes: map[string][]string{}, Cleaned: map[string]json.RawMessage{}}
	rollback := errors.New("independent form observation rollback")
	err = native.database.AtomicRelation(t.Context(), func(session db.RelationSession) error {
		ctx := t.Context()
		if err := native.revoke(ctx, session, caseInput.Setup.Revoke); err != nil {
			return err
		}
		current, err := native.row(ctx, session)
		if err != nil {
			return err
		}
		proof.Before = adminChoiceStored(current)
		if input.Bound {
			bound, err := native.bind(ctx, session, caseInput.Actor, caseInput.Relation, forms.NewData(input.Data), current)
			if err != nil {
				return err
			}
			form := bound.Form()
			valid := form.Valid()
			proof.Valid = &valid
			proof.Codes, proof.Cleaned, err = adminChoiceFormValues(form)
			if err != nil {
				return err
			}
			if valid {
				if err := native.save(ctx, session, caseInput.Relation, bound, current); err != nil {
					return err
				}
				proof.Saved = true
			}
		} else {
			spec, err := native.spec(ctx, session, caseInput.Actor, caseInput.Relation, forms.NewData(nil), adminChoiceInitial(current), true)
			if err != nil {
				return err
			}
			// Model binding retains all stored scalar values. The ordinary Form
			// accepts only this observation's selected relation as display initial.
			initial := adminChoiceInitial(current)
			if _, err := spec.Unbound(map[string]forms.Value{caseInput.Relation: initial[caseInput.Relation]}); err != nil {
				return err
			}
		}
		after, err := native.row(ctx, session)
		if err != nil {
			return err
		}
		proof.After = adminChoiceStored(after)
		return rollback
	})
	if err != rollback {
		t.Fatalf("form observation did not return its exact rollback cause: %v", err)
	}
	final, err := native.domain(t.Context(), native.database)
	if err != nil {
		t.Fatal(err)
	}
	proof.RolledBack = reflect.DeepEqual(original, final)
	if !proof.RolledBack {
		t.Fatal("form observation changed domain state after rollback")
	}
	return proof
}

func adminChoiceNormalizeCleaned(t *testing.T, values map[string]json.RawMessage) map[string]any {
	t.Helper()
	out := map[string]any{}
	for name, raw := range values {
		if name == "many" {
			var keys []string
			if err := adminChoiceDecode(raw, &keys); err != nil {
				t.Fatal(err)
			}
			slices.SortFunc(keys, func(a, b string) int {
				av, bv := adminChoiceInt(t, a), adminChoiceInt(t, b)
				if av < bv {
					return -1
				}
				if av > bv {
					return 1
				}
				return 0
			})
			out[name] = keys
		} else {
			var key *string
			if err := adminChoiceDecode(raw, &key); err != nil {
				t.Fatal(err)
			}
			out[name] = key
		}
	}
	return out
}
func adminChoiceCompareForm(t *testing.T, got adminChoiceFormProof, want adminChoiceFormResult) {
	t.Helper()
	if !reflect.DeepEqual(got.Valid, want.Valid) || !reflect.DeepEqual(got.Codes, want.Codes) || !reflect.DeepEqual(adminChoiceNormalizeCleaned(t, got.Cleaned), adminChoiceNormalizeCleaned(t, want.Cleaned)) || got.Saved != want.Saved || !reflect.DeepEqual(got.Before, want.Before) || !reflect.DeepEqual(got.After, want.After) || got.RolledBack != want.RolledBack {
		t.Fatalf("native form and independent Django form differ\ngot: %+v\nwant: %+v", got, want)
	}
}

// Public Admin routes use one projected relation per case. The final writer
// rebinds submitted data under its own transaction and returns only after the
// outer commit succeeds. This HTTP path is separate from the rollback oracle.
type adminChoiceWriteStats struct {
	Calls   int `json:"calls"`
	Commits int `json:"commits"`
}

func (native *adminChoiceNative) client(t *testing.T, c adminChoiceCase) (*siteHTTPClient, *adminChoiceWriteStats) {
	t.Helper()
	stats := &adminChoiceWriteStats{}
	installed, err := apps.New([]apps.Config{{Name: "choice_reference", Label: fixture.App}})
	if err != nil {
		t.Fatal(err)
	}
	permission := func(name string) auth.Permission { return mustPermission(t, fixture.App+"."+name) }
	var grants []auth.Permission
	for _, op := range c.Actor.Source {
		grants = append(grants, permission(op+"_source"))
	}
	for _, op := range c.Actor.Target {
		grants = append(grants, permission(op+"_target"))
	}
	principal := sitePrincipal(t, "choice-observer", true, grants...)
	choices, err := native.choices(c.Relation)
	if err != nil {
		t.Fatal(err)
	}
	related, err := NewRelatedChoices(RelatedChoiceConfig[fixture.Target]{Choices: choices, Permission: permission("view_target"), Scope: func(ctx context.Context, actor auth.Principal, origin ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[fixture.Target]) error) error {
		if actor.ID() != principal.ID() || origin.Source() != (ir.ModelIdentity{AppLabel: fixture.App, ModelName: "source"}) {
			return errors.New("choice observer source/actor mismatch")
		}
		predicates, err := native.predicates(c.Actor)
		if err != nil {
			return err
		}
		return visit(native.database, predicates)
	}})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := native.models.Sources.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	config := ModelConfig[adminChoiceRow]{AppLabel: fixture.App, Slug: "sources", Model: metadata, FormFields: []string{c.Relation}, RelatedChoices: []RelatedChoices{related}, ListFields: []string{"id"},
		Permissions: Permissions{View: permission("view_source"), Add: permission("add_source"), Change: permission("change_source"), Delete: permission("delete_source")},
		List: func(ctx context.Context, _ auth.Principal, request ListRequest) (Page[adminChoiceRow], error) {
			row, err := native.row(ctx, native.database)
			return Page[adminChoiceRow]{Items: []adminChoiceRow{row}, Total: 1, Offset: request.Offset, Limit: request.Limit}, err
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (adminChoiceRow, bool, error) {
			if id != 1 {
				return adminChoiceRow{}, false, nil
			}
			row, err := native.row(ctx, native.database)
			return row, err == nil, err
		},
		Snapshot: func(row adminChoiceRow) (Object, error) {
			many := make([]templates.Value, len(row.Many))
			for i, key := range row.Many {
				many[i] = templates.Integer(key)
			}
			return NewObject(row.Source.ID, "Source", map[string]templates.Value{"id": templates.Integer(row.Source.ID), "tenant": templates.Integer(row.Source.Tenant), "foreign_key": templates.Integer(row.Source.ForeignKey), "one_to_one": templates.Integer(row.Source.OneToOne), "many": templates.List(many...)})
		},
		Initial: func(row adminChoiceRow) (map[string]forms.Value, error) { return adminChoiceInitial(row), nil },
		Create: func(context.Context, auth.Principal, formmodel.BoundForm, InlineSubmission) (MutationResult, error) {
			return MutationResult{}, errors.New("fixture only observes add projection")
		},
		Delete: func(context.Context, auth.Principal, Mutation) (MutationResult, error) {
			return MutationResult{}, errors.New("fixture does not expose source deletion")
		},
	}
	config.Update = func(ctx context.Context, actor auth.Principal, mutation Mutation, submitted formmodel.BoundForm, _ InlineSubmission) (MutationResult, []string, error) {
		stats.Calls++
		var result adminChoiceRow
		var changed []string
		err := native.database.AtomicRelation(ctx, func(session db.RelationSession) error {
			for _, name := range []string{"change_source", "view_target"} {
				allowed, err := (auth.PrincipalAuthorizer{}).Allowed(ctx, actor, permission(name))
				if err != nil {
					return err
				}
				if !allowed {
					return NewOperationError(OperationDenied, nil)
				}
			}
			if mutation.ID != 1 {
				return ErrObjectNotFound
			}
			current, err := native.row(ctx, session)
			if err != nil {
				return err
			}
			bound, err := native.bind(ctx, session, c.Actor, c.Relation, submitted.Form().Submitted(), current)
			if err != nil {
				return err
			}
			if !bound.Form().Valid() {
				return validation.Reject(bound.Form().Errors(), nil)
			}
			if err := native.save(ctx, session, c.Relation, bound, current); err != nil {
				return err
			}
			result, err = native.row(ctx, session)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(adminChoiceStored(current), adminChoiceStored(result)) {
				changed = []string{c.Relation}
			}
			return nil
		})
		if err == nil {
			stats.Commits++
		}
		return MutationResult{ID: result.Source.ID}, changed, err
	}
	builder := NewBuilder(installed)
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	runtime, _ := siteTestRuntime(t, registry, allowed, "/admin/login/", "/admin/", []siteIdentity{{username: "observer", principal: principal}})
	site, err := NewSite(SiteConfig{Apps: installed, Namespace: fixture.App, Registry: registry, Auth: runtime})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "choice_reference", InstalledApps: installed.All()})
	if err != nil {
		t.Fatal(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	if err != nil {
		t.Fatal(err)
	}
	client := newSiteHTTPClient(application)
	client.login(t, "observer", "secret", "/admin/")
	return client, stats
}

func adminChoiceOptions(body, field string) ([]adminChoiceOption, bool, error) {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	options := []adminChoiceOption{}
	disabled := false
	found := 0
	attr := func(node *html.Node, name string) (string, bool) {
		for _, a := range node.Attr {
			if a.Key == name {
				return a.Val, true
			}
		}
		return "", false
	}
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, inSelect bool) {
		if node.Type == html.ElementNode && node.Data == "select" {
			name, _ := attr(node, "name")
			inSelect = name == field
			if inSelect {
				found++
				_, disabled = attr(node, "disabled")
			}
		}
		if inSelect && node.Type == html.ElementNode && node.Data == "option" {
			key, _ := attr(node, "value")
			_, selected := attr(node, "selected")
			var label strings.Builder
			var text func(*html.Node)
			text = func(n *html.Node) {
				if n.Type == html.TextNode {
					label.WriteString(n.Data)
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					text(child)
				}
			}
			text(node)
			options = append(options, adminChoiceOption{key, label.String(), selected})
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inSelect)
		}
	}
	walk(root, false)
	if found > 1 {
		return nil, false, errors.New("duplicate native choice control")
	}
	return options, disabled, nil
}

// A source-view-only request uses Admin's detail document, with no mutation
// control. Observe that actual representation instead of inventing a disabled
// select. The logout form is unrelated to the source's write authority.
func adminChoiceDetail(body, field string) (bool, string, error) {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return false, "", err
	}
	attr := func(node *html.Node, name string) string {
		for _, a := range node.Attr {
			if a.Key == name {
				return a.Val
			}
		}
		return ""
	}
	detail, fields, editable := false, 0, 0
	var value strings.Builder
	var content func(*html.Node)
	content = func(node *html.Node) {
		if node.Type == html.TextNode {
			value.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			content(child)
		}
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Data == "body" && attr(node, "data-admin-view") == "detail" {
				detail = true
			}
			if node.Data == "dd" && attr(node, "data-field-name") == field {
				fields++
				content(node)
			}
			if (node.Data == "input" || node.Data == "select" || node.Data == "textarea") && attr(node, "name") == field {
				editable++
			}
			if node.Data == "form" && strings.HasPrefix(attr(node, "action"), "/admin/sources/change/") {
				editable++
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if detail && (fields != 1 || editable != 0) {
		return false, "", errors.New("readonly detail has missing/duplicate data or an editable source control")
	}
	if !detail {
		return false, "", nil
	}
	return true, value.String(), nil
}

func adminChoiceDetailNegatives(t *testing.T) {
	t.Helper()
	const prefix = `<body data-admin-view="detail"><dd data-field-name="foreign_key">9007199254740993 &amp; 7</dd>`
	detail, value, err := adminChoiceDetail(prefix+`<form action="/admin/logout/"><input name="csrfmiddlewaretoken"></form></body>`, "foreign_key")
	if err != nil || !detail || value != "9007199254740993 & 7" {
		t.Fatal("readonly detail positive control", err)
	}
	for _, extra := range []string{`<select name="foreign_key"></select>`, `<input name="foreign_key">`, `<textarea name="foreign_key"></textarea>`, `<form action="/admin/sources/change/?id=1"></form>`, `<dd data-field-name="foreign_key">other</dd>`} {
		if _, _, err := adminChoiceDetail(prefix+extra+`</body>`, "foreign_key"); err == nil {
			t.Fatal("readonly detail accepted writable or duplicated markup")
		}
	}
	if _, _, err := adminChoiceDetail(`<body data-admin-view="detail"></body>`, "foreign_key"); err == nil {
		t.Fatal("readonly detail accepted missing field")
	}
}
