package articleapp_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
)

func TestPreparedArticleUpdateOwnsEncodingHookSnapshotsAndRollback(t *testing.T) {
	for _, operation := range []string{"update", "patch"} {
		for _, mode := range []string{"success", "no-op", "prepare failure", "output limit", "hook failure", "cancel prepare", "nil prepare"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ctx, _, repository := openArticleRepository(t, t.Name())
				summary := "original summary"
				created, err := repository.Create(ctx, articleapp.Input{Title: "Original", Summary: &summary})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()
				private := errors.New("private prepare or hook failure")
				var stages []string
				hooked := repository.WithMutationHook(func(_ context.Context, _ db.Session, result articleapp.MutationResult) error {
					stages = append(stages, "hook")
					if len(result.Items) != 1 || result.Items[0].Article.Summary == nil || *result.Items[0].Article.Summary != summary || fmt.Sprint(result.Items[0].ChangedFields) != "[title]" {
						t.Error("prepare mutated hook snapshot", result)
					}
					if mode == "hook failure" {
						return private
					}
					return nil
				})
				limits := serializers.Limits{}
				if mode == "output limit" {
					limits.MaxDocumentBytes = 4
				}
				response, err := output.New(output.String(), limits)
				if err != nil {
					t.Fatal(err)
				}
				prepare := func(work context.Context, article articleapp.Article, fields []string) (output.Prepared[string], error) {
					stages = append(stages, "prepare")
					if mode == "no-op" && len(fields) != 0 {
						t.Error("no-op manufactured a mutation")
					}
					if article.Summary != nil {
						*article.Summary = "mutated callback input"
					}
					if len(fields) != 0 {
						fields[0] = "mutated callback fields"
					}
					prepared, err := response.Prepare(work, 200, article.Title)
					if mode == "prepare failure" {
						return prepared, private
					}
					if mode == "cancel prepare" {
						cancel()
					}
					return prepared, err
				}
				if mode == "nil prepare" {
					prepare = nil
				}
				title := "Changed"
				if mode == "no-op" {
					title = "Original"
				}
				var result output.Prepared[string]
				if operation == "update" {
					result, err = articleapp.UpdateAndPrepare(ctx, hooked, created.ID, articleapp.Input{Title: title, Summary: &summary}, prepare)
				} else {
					result, err = articleapp.PatchAndPrepare(ctx, hooked, created.ID, (articleapp.Patch{}).WithTitle(title), prepare)
				}
				ok := mode == "success" || mode == "no-op"
				if (err == nil) != ok {
					t.Fatal("operation outcome", err)
				}
				encoded, encodeErr := response.Response(result)
				if (encodeErr == nil) != ok || !ok && encoded.Status() != 0 {
					t.Fatal("unconfirmed prepared value escaped", encodeErr)
				}
				if (mode == "prepare failure" || mode == "hook failure") && !errors.Is(err, private) {
					t.Fatal("failure cause lost", err)
				}
				if mode == "cancel prepare" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancelled preparation succeeded", err)
				}
				wantStages := []string{"prepare"}
				if mode == "success" || mode == "hook failure" {
					wantStages = append(wantStages, "hook")
				}
				if mode == "nil prepare" {
					wantStages = nil
				}
				if !reflect.DeepEqual(stages, wantStages) {
					t.Fatal("hook/prepare order", stages)
				}
				stored, found, err := repository.Get(context.Background(), created.ID)
				if err != nil || !found || stored.Summary == nil || *stored.Summary != summary {
					t.Fatal("stored snapshot changed", stored, err)
				}
				wantTitle := created.Title
				if ok {
					wantTitle = title
				}
				if stored.Title != wantTitle {
					t.Fatal("failed preparation committed a write", stored)
				}
			})
		}
	}
}

type preparedAtomicBackend struct {
	articleapp.Backend
	run func(context.Context, func(db.Session) error) error
}

func (backend preparedAtomicBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return backend.run(ctx, callback)
}

func TestPreparedArticleUpdateRejectsUnownedAtomicCompletion(t *testing.T) {
	for _, mode := range []string{"skipped", "nil session", "typed nil session", "repeated", "concurrent", "retained", "unfinished", "swallowed miss", "unknown commit", "joined miss", "wrapped miss", "confirmed miss", "confirmed late cancel", "begin failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, native, repository := openArticleRepository(t, t.Name())
			created, err := repository.Create(ctx, articleapp.Input{Title: "Original"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			private := errors.New("private transaction boundary")
			var retained func(db.Session) error
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			backend := preparedAtomicBackend{Backend: native}
			backend.run = func(ctx context.Context, callback func(db.Session) error) error {
				switch mode {
				case "skipped":
					return nil
				case "retained":
					retained = callback
					return nil
				case "begin failure":
					return private
				case "unfinished":
					go func() { finished <- native.Atomic(ctx, callback) }()
					select {
					case <-started:
					case <-time.After(5 * time.Second):
						return errors.New("preparation did not start")
					}
					return nil
				}
				err := native.Atomic(ctx, func(session db.Session) error {
					switch mode {
					case "nil session":
						return callback(nil)
					case "typed nil session":
						var absent *typedNilPreparedSession
						return callback(absent)
					case "repeated":
						return errors.Join(callback(session), callback(session))
					case "concurrent":
						go func() { finished <- callback(session) }()
						<-started
						second := callback(session)
						close(release)
						return errors.Join(second, <-finished)
					default:
						return callback(session)
					}
				})
				switch mode {
				case "swallowed miss":
					return nil
				case "unknown commit":
					return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Cause: private}
				case "joined miss":
					return errors.Join(err, private)
				case "wrapped miss":
					return fmt.Errorf("transaction boundary: %w", err)
				case "confirmed late cancel":
					cancel()
				}
				return err
			}
			wrapped, err := articleapp.NewRepository(backend)
			if err != nil {
				t.Fatal(err)
			}
			id := created.ID
			if mode == "swallowed miss" || mode == "joined miss" || mode == "wrapped miss" || mode == "confirmed miss" {
				id++
			}
			result, err := articleapp.PatchAndPrepare(ctx, wrapped, id, articleapp.Patch{}, func(context.Context, articleapp.Article, []string) (string, error) {
				if mode == "concurrent" || mode == "unfinished" {
					close(started)
					<-release
				}
				return "prepared success", nil
			})
			if mode == "unfinished" {
				close(release)
				select {
				case failure := <-finished:
					if !errors.Is(failure, context.Canceled) {
						t.Error("unjoined callback kept running", failure)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("unjoined callback did not exit")
				}
			}
			if mode == "confirmed late cancel" {
				if err != nil || result != "prepared success" || ctx.Err() == nil {
					t.Fatal("confirmed commit was overturned", result, err)
				}
			} else if err == nil || result != "" {
				t.Fatal("unconfirmed completion escaped", result, err)
			}
			if mode == "skipped" || mode == "repeated" || mode == "concurrent" || mode == "retained" || mode == "unfinished" || mode == "swallowed miss" {
				if !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
					t.Fatal("ownership failure lost unknown-outcome marker", err)
				}
			}
			if mode == "begin failure" || mode == "joined miss" || mode == "unknown commit" {
				if !errors.Is(err, private) {
					t.Fatal("private cause lost", err)
				}
			}
			if mode == "confirmed miss" {
				if missing, ok := err.(*articleapp.Error); !ok || missing.Code != articleapp.CodeNotFound || missing.Cause != nil {
					t.Fatal("confirmed miss lost direct rejection", err)
				}
			}
			if mode == "joined miss" || mode == "wrapped miss" {
				if _, ok := err.(*articleapp.Error); ok {
					t.Fatal("boundary failure became direct not-found", err)
				}
			}
			if retained != nil {
				if err := native.Atomic(context.Background(), retained); err == nil {
					t.Fatal("retained callback executed after its owner")
				}
			}
			stored, found, readErr := repository.Get(context.Background(), created.ID)
			if readErr != nil || !found || stored.Title != created.Title {
				t.Fatal("ownership test changed data", readErr)
			}
		})
	}
}

type typedNilPreparedSession struct{ db.Session }
