package articleapp_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
)

func TestPreparedArticleCreateKeepsOutputHookAndCommitInOneOwnedWrite(t *testing.T) {
	for _, mode := range []string{"success", "prepare failure", "hook failure", "cancel preparation", "nil preparation", "unknown commit", "late cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, native, plain := openArticleRepository(t, t.Name())
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			private := errors.New("private create preparation failure")
			attempts := 0
			backend := preparedAtomicBackend{Backend: native, run: func(ctx context.Context, callback func(db.Session) error) error {
				attempts++
				err := native.Atomic(ctx, callback)
				if err == nil && mode == "unknown commit" {
					return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
				}
				if err == nil && mode == "late cancel" {
					cancel()
				}
				return err
			}}
			repository, err := articleapp.NewRepository(backend)
			if err != nil {
				t.Fatal(err)
			}
			var stages []string
			repository = repository.WithMutationHook(func(work context.Context, session db.Session, mutation articleapp.MutationResult) error {
				stages = append(stages, "hook")
				if mutation.Operation != articleapp.MutationCreate || len(mutation.Items) != 1 || mutation.Items[0].Article.Summary == nil || *mutation.Items[0].Article.Summary != "owned" {
					t.Fatal("preparation changed the mutation snapshot", mutation)
				}
				if err := session.(db.SessionValidator).ValidateSession(work); err != nil {
					t.Fatal(err)
				}
				if mode == "hook failure" {
					return private
				}
				return nil
			})
			prepare := func(work context.Context, value articlemodels.Article) (string, error) {
				stages = append(stages, "prepare")
				if value.ID <= 0 || value.Title != "created" || value.Published {
					t.Fatal("model default or generated key lost", value)
				}
				*value.Summary = "only the callback copy"
				if mode == "prepare failure" {
					return "must not escape", private
				}
				if mode == "cancel preparation" {
					cancel()
				}
				return "prepared", nil
			}
			if mode == "nil preparation" {
				prepare = nil
			}
			input := partialArticleInput(t, map[string]query.Value{"title": query.String("created"), "summary": query.String("owned")})
			result, err := articleapp.CreateAndPrepare(ctx, repository, input, prepare)
			ok := mode == "success" || mode == "late cancel"
			if (err == nil) != ok || ok && result != "prepared" || !ok && result != "" {
				t.Fatal("unconfirmed create result", result, err)
			}
			if (mode == "prepare failure" || mode == "hook failure") && !errors.Is(err, private) {
				t.Fatal("lost failure identity", err)
			}
			if mode == "cancel preparation" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation", err)
			}
			if mode == "unknown commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("lost uncertain commit", err)
			}
			wantStages := []string{"prepare", "hook"}
			wantAttempts := 1
			if mode == "prepare failure" || mode == "cancel preparation" {
				wantStages = []string{"prepare"}
			}
			if mode == "nil preparation" {
				wantStages, wantAttempts = nil, 0
			}
			if !slices.Equal(stages, wantStages) || attempts != wantAttempts {
				t.Fatal("execution order or retry", stages, attempts)
			}
			page, err := plain.List(context.Background(), articleapp.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if ok || mode == "unknown commit" {
				want = 1
			}
			if page.Total != want {
				t.Fatal("failed preparation committed a row", page.Total)
			}
			if want == 1 && (page.Articles[0].Summary == nil || *page.Articles[0].Summary != "owned") {
				t.Fatal("callback copy changed stored data")
			}
		})
	}
}
