package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/godj-model-save/models"
	"example.com/godj-model-save/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
)

//go:embed reference.json
var reference []byte

type probeBackend interface {
	db.Session
	db.RelationAtomic
	db.CoordinatedRelationAtomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

type nativeCase struct {
	Name    string              `json:"name"`
	Valid   bool                `json:"valid"`
	Errors  map[string][]string `json:"errors"`
	Prepare struct {
		OK         bool `json:"ok"`
		QueryCount int  `json:"query_count"`
	} `json:"prepare"`
	Unsaved *struct {
		OK         bool `json:"ok"`
		QueryCount int  `json:"query_count"`
	} `json:"unsaved_collections"`
	Save struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		HasKey bool   `json:"has_key"`
	} `json:"save"`
	Stored []storedArticle `json:"stored"`
}
type storedArticle struct {
	Title     string   `json:"title"`
	Hidden    string   `json:"hidden"`
	Labels    []string `json:"labels"`
	Reviewers []string `json:"reviewers"`
}

func TestGeneratedModelFormSave(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		b, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "form.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		runSave(t, b)
	})
	dsn := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if dsn == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), dsn)
		if err != nil {
			t.Fatal("connect form save reference DB")
		}
		name := fmt.Sprintf("godj_form_save_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err = connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
			_ = connection.Close(context.Background())
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				t.Error(err)
			}
			if err := connection.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		b, err := postgres.Open(t.Context(), postgres.Config{URL: dsn, Schema: name})
		if err != nil {
			t.Fatal("open form save reference DB")
		}
		runSave(t, b)
	})
}

func runSave(t *testing.T, b probeBackend) {
	t.Helper()
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	var fixture struct {
		Cases []nativeCase `json:"cases"`
	}
	if err := json.Unmarshal(reference, &fixture); err != nil || len(fixture.Cases) != 15 {
		t.Fatal("native fixture", err)
	}
	metadata := models.ArticleDescriptor{}.Metadata()
	document, err := definition.Encode(definition.Producer{Name: "model-save-reference", Version: "1"}, migrations.Migration{App: "saveprobe", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "saveprobe", Model: models.LabelDescriptor{}.Metadata()}, migrations.CreateModel{AppLabel: "saveprobe", Model: metadata},
	}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "save/0001_initial", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (migrations.Executor{Backend: b}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	labelsSaver, err := formmodel.SaveManyToMany(collections.ModelsArticleLabels)
	if err != nil {
		t.Fatal(err)
	}
	reviewersSaver, err := formmodel.SaveManyToMany(collections.ModelsArticleReviewers)
	if err != nil {
		t.Fatal(err)
	}
	reset := func(t *testing.T) {
		t.Helper()
		articles, err := models.ArticleObjects.Using(b).OrderBy(models.ArticleFields.ID.Asc()).All(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, article := range articles {
			if _, err := deleters.ModelsArticle.Delete(t.Context(), b, &article); err != nil {
				t.Fatal(err)
			}
		}
		labels, err := models.LabelObjects.Using(b).OrderBy(models.LabelFields.ID.Asc()).All(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, label := range labels {
			if _, err := deleters.ModelsLabel.Delete(t.Context(), b, &label); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot := func(t *testing.T) []storedArticle {
		t.Helper()
		result := []storedArticle{}
		articles, err := models.ArticleObjects.Using(b).OrderBy(models.ArticleFields.ID.Asc()).All(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, article := range articles {
			row := storedArticle{Title: article.Title, Hidden: article.Hidden, Labels: []string{}, Reviewers: []string{}}
			labels, err := collections.ModelsArticleLabels.From(b, article)
			if err != nil {
				t.Fatal(err)
			}
			labelQuery, err := labels.Query()
			if err != nil {
				t.Fatal(err)
			}
			all, err := labelQuery.OrderBy(models.LabelFields.Code.Asc()).All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, label := range all {
				row.Labels = append(row.Labels, label.Code)
			}
			reviewers, err := collections.ModelsArticleReviewers.From(b, article)
			if err != nil {
				t.Fatal(err)
			}
			reviewerQuery, err := reviewers.Query()
			if err != nil {
				t.Fatal(err)
			}
			all, err = reviewerQuery.OrderBy(models.LabelFields.Code.Asc()).All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, label := range all {
				row.Reviewers = append(row.Reviewers, label.Code)
			}
			result = append(result, row)
		}
		return result
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			reset(t)
			labels := map[string]models.Label{}
			for _, code := range []string{"a", "b", "c"} {
				value, err := models.LabelObjects.Create(t.Context(), b, models.NewLabelCreate(code))
				if err != nil {
					t.Fatal(err)
				}
				labels[code] = value
			}
			var current *models.Article
			if strings.HasPrefix(test.Name, "existing_") {
				value, err := models.ArticleObjects.Create(t.Context(), b, models.NewArticleCreate("old").WithHidden("stored"))
				if err != nil {
					t.Fatal(err)
				}
				current = &value
				for _, saver := range []formmodel.CollectionSaver[models.Article]{labelsSaver, reviewersSaver} {
					if err := saver.Save(t.Context(), b, value, []int64{labels["a"].ID}); err != nil {
						t.Fatal(err)
					}
				}
			}
			fields := []string{"title", "labels", "reviewers"}
			savers := []formmodel.CollectionSaver[models.Article]{reviewersSaver, labelsSaver}
			if test.Name == "existing_excluded" {
				fields = []string{"title"}
				savers = nil
			}
			spec, err := formmodel.NewSpecForFields(metadata, fields)
			if err != nil {
				t.Fatal(err)
			}
			choices := []forms.Choice{}
			for _, code := range []string{"a", "b", "c"} {
				choices = append(choices, forms.Choice{Value: forms.Integer(labels[code].ID), Label: code})
			}
			for _, name := range fields[1:] {
				spec, err = spec.WithModelChoices(name, choices...)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw := map[string][]string{"title": {"changed"}, "labels": {strconv.FormatInt(labels["b"].ID, 10)}, "reviewers": {strconv.FormatInt(labels["c"].ID, 10)}}
			if test.Name == "existing_clear" {
				raw["labels"] = nil
				raw["reviewers"] = nil
			}
			if test.Name == "invalid_scalar" {
				raw["title"] = []string{""}
			}
			if test.Name == "invalid_choice" {
				raw["labels"] = []string{"999999999"}
			}
			bound, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(raw), current, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			codes := map[string][]string{}
			for _, failure := range bound.BoundForm().Form().Errors().All() {
				codes[string(failure.Field())] = append(codes[string(failure.Field())], string(failure.Code()))
			}
			if bound.BoundForm().Form().Valid() != test.Valid || !reflect.DeepEqual(codes, test.Errors) {
				t.Fatal("native validation differs", codes, test.Errors)
			}
			before := snapshot(t)
			prepared, prepareErr := bound.Prepare()
			if (prepareErr == nil) != test.Prepare.OK || test.Prepare.QueryCount != 0 {
				t.Fatal("native preparation differs", prepareErr)
			}
			if !reflect.DeepEqual(before, snapshot(t)) {
				t.Fatal("prepare wrote data")
			}
			var value models.Article
			if prepareErr == nil {
				value, err = prepared.Model()
				if err != nil {
					t.Fatal(err)
				}
			}
			failure := errors.New("synthetic second collection failure")
			if strings.Contains(test.Name, "collection_failure") {
				savers[0].Save = func(context.Context, db.Session, models.Article, []int64) error { return failure }
			}
			if test.Unsaved != nil {
				err := prepared.SaveCollections(t.Context(), b, value, savers...)
				var keyError *formmodel.Error
				if !errors.As(err, &keyError) || keyError.Code != "primary_key_required" || test.Unsaved.OK || test.Unsaved.QueryCount != 0 || !reflect.DeepEqual(before, snapshot(t)) {
					t.Fatal("unsaved collection boundary", err)
				}
			}
			if strings.Contains(test.Name, "late_deleted_choice") {
				target := labels["b"]
				if _, err := deleters.ModelsLabel.Delete(t.Context(), b, &target); err != nil {
					t.Fatal(err)
				}
			}
			save := func(backend db.Session) error {
				if prepareErr != nil {
					return prepareErr
				}
				if strings.Contains(test.Name, "deferred") {
					value.Title = "server-edited"
					if err := models.ArticleObjects.Save(t.Context(), backend, &value); err != nil {
						return err
					}
					return prepared.SaveCollections(t.Context(), backend, value, savers...)
				}
				if err := prepared.Save(t.Context(), backend, &value, savers...); err != nil {
					return err
				}
				if test.Name == "new_repeat" {
					return prepared.Save(t.Context(), backend, &value, savers...)
				}
				return nil
			}
			if strings.HasSuffix(test.Name, "_atomic") {
				err = b.AtomicRelation(t.Context(), func(session db.RelationSession) error { return save(session) })
			} else {
				err = save(b)
			}
			_, hasKey := (models.ArticleDescriptor{}).PrimaryKey(value)
			if (err == nil) != test.Save.OK || hasKey != test.Save.HasKey {
				t.Fatal("native save/key outcome differs", err, hasKey, test.Save)
			}
			if test.Save.Error == "RuntimeError" && !errors.Is(err, failure) {
				t.Fatal("collection failure type lost", err)
			}
			if test.Save.Error == "IntegrityError" {
				var dbError *query.Error
				// GoDj preserves its existing literal-COMMIT uncertainty contract.
				// The following fresh read reconciles test storage; it is not an
				// automatic retry or a downgrade to Django IntegrityError.
				if !errors.As(err, &dbError) || dbError.Category != query.CategoryBackend || dbError.Code != query.CodeCommitOutcomeUnknown {
					t.Fatal("literal COMMIT uncertainty was weakened", err)
				}
			}
			if actual := snapshot(t); !reflect.DeepEqual(actual, test.Stored) {
				t.Fatalf("native stored state differs: got %+v; want %+v", actual, test.Stored)
			}
		})
	}
	for _, mode := range []string{"transaction_guard_denied", "transaction_guard_stale", "transaction_choice_changed", "transaction_callback_failure_after_write", "expired_session", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			reset(t)
			target, err := models.LabelObjects.Create(t.Context(), b, models.NewLabelCreate("allowed"))
			if err != nil {
				t.Fatal(err)
			}
			current, err := models.ArticleObjects.Create(t.Context(), b, models.NewArticleCreate("revision-one"))
			if err != nil {
				t.Fatal(err)
			}
			spec, err := formmodel.NewSpecForFields(metadata, []string{"title", "labels"})
			if err != nil {
				t.Fatal(err)
			}
			spec, err = spec.WithModelChoices("labels", forms.Choice{Value: forms.Integer(target.ID), Label: target.Code})
			if err != nil {
				t.Fatal(err)
			}
			bound, err := formmodel.BindInstance(t.Context(), models.ArticleObjects, spec, forms.NewData(map[string][]string{"title": {"candidate"}, "labels": {strconv.FormatInt(target.ID, 10)}}), &current, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := bound.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			value, err := prepared.Model()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "transaction_guard_stale" {
				fresh := current
				fresh.Title = "revision-two"
				if err := models.ArticleObjects.Save(t.Context(), b, &fresh); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "transaction_choice_changed" {
				target.Code = "denied"
				if err := models.LabelObjects.Save(t.Context(), b, &target); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshot(t)
			rejected := errors.New("current write admission rejected")
			calls := 0
			guarded := labelsSaver
			guarded.Save = func(ctx context.Context, backend db.Session, owner models.Article, keys []int64) error {
				calls++
				choice, found, err := models.LabelObjects.Using(backend).Filter(models.LabelFields.ID.Exact(target.ID)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
				if err != nil {
					return err
				}
				if !found || choice.Code != "allowed" {
					return rejected
				}
				if err := labelsSaver.Save(ctx, backend, owner, keys); err != nil {
					return err
				}
				if mode == "transaction_callback_failure_after_write" {
					return rejected
				}
				return nil
			}
			var retained db.RelationSession
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			err = b.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error {
				retained = session
				if mode == "expired_session" {
					return nil
				}
				if mode == "transaction_guard_denied" {
					return rejected
				}
				fresh, found, err := models.ArticleObjects.Using(session).Filter(models.ArticleFields.ID.Exact(current.ID)).OrderBy(models.ArticleFields.ID.Asc()).First(ctx)
				if err != nil {
					return err
				}
				if !found || fresh.Title != current.Title {
					return rejected
				}
				return prepared.Save(ctx, session, &value, guarded)
			})
			if mode == "expired_session" {
				if err != nil {
					t.Fatal(err)
				}
				err = prepared.Save(t.Context(), retained, &value, guarded)
				if err == nil || calls != 0 {
					t.Fatal("expired session used", err, calls)
				}
			} else if mode == "canceled" {
				if !errors.Is(err, context.Canceled) || calls != 0 {
					t.Fatal("cancellation changed", err, calls)
				}
			} else if !errors.Is(err, rejected) {
				t.Fatal("admission/late error lost", err)
			}
			if (mode == "transaction_guard_denied" || mode == "transaction_guard_stale") && calls != 0 {
				t.Fatal("rejected row reached collection")
			}
			if !reflect.DeepEqual(before, snapshot(t)) {
				t.Fatal("failed transaction left scalar or collection data")
			}
		})
	}
	t.Run("formset", func(t *testing.T) {
		runSetSave(t, b, reset, snapshot, labelsSaver, reviewersSaver, func(ctx context.Context, backend db.Session, value models.Article) error {
			if _, borrowed := backend.(db.SessionValidator); borrowed {
				session, ok := backend.(db.RelationSession)
				if !ok {
					return errors.New("lost relation scope")
				}
				_, err := deleters.ModelsArticle.DeleteInSession(ctx, session, value)
				return err
			}
			atomic, ok := backend.(db.RelationAtomic)
			if !ok {
				return errors.New("lost atomic backend")
			}
			_, err := deleters.ModelsArticle.Delete(ctx, atomic, &value)
			return err
		})
	})
	reset(t)
}
