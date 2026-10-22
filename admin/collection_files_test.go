package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	article "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

// Reuse the generated string storage with explicit canonical file metadata.
// Generated FileField ABI/native DB persistence belongs to the external file
// consumer; this test covers the Admin current-binding and upload lifetime.
type collectionFileDescriptor struct {
	article.ArticleDescriptor
	model ir.Model
}

func (d collectionFileDescriptor) Metadata() ir.Model { return d.model.Clone() }

type collectionFileStorage struct {
	storage.Backend
	mode           string
	saves, deletes int
}

func (s *collectionFileStorage) Save(ctx context.Context, name string, input io.Reader, options storage.SaveOptions) (storage.Info, error) {
	s.saves++
	if s.saves == 2 && s.mode == "partial_publish" {
		return storage.Info{}, &storage.Error{Code: "write_failed", Outcome: storage.NotPublished}
	}
	info, err := s.Backend.Save(ctx, name, input, options)
	if err == nil && s.saves == 2 && s.mode == "unknown_publish" {
		return info, &storage.Error{Code: "publication_unknown", Outcome: storage.Uncertain}
	}
	return info, err
}
func (s *collectionFileStorage) Delete(ctx context.Context, name string) error {
	s.deletes++
	return s.Backend.Delete(ctx, name)
}

func TestAdminCollectionFilesRetainCurrentBindingPublicationAndRequestLifetime(t *testing.T) {
	for _, mode := range []string{"replace_and_create", "readonly_and_create", "preserve", "clear", "deleted_upload", "contradiction", "duplicate_file", "unknown_file", "primary_file", "noncanonical_row", "denied_add", "current_rejection", "late_writer_failure", "partial_publish", "unknown_publish"} {
		t.Run(mode, func(t *testing.T) {
			config := validRegistryConfig(t)
			metadata := (article.ArticleDescriptor{}).Metadata()
			for index := range metadata.Fields {
				field := &metadata.Fields[index]
				if field.Name == "title" || field.Name == "summary" {
					field.Kind, field.Blank, field.MaxLength = ir.FieldFile, true, 100
				}
			}
			binding, err := orm.BindProject(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: config.AppLabel, Models: []ir.Model{metadata}})
			if err != nil {
				t.Fatal(err)
			}
			manager := orm.NewManager[article.Article](collectionFileDescriptor{model: metadata})
			config.Model, config.FormFields, config.SearchFields = metadata, []string{"published"}, nil
			policy := forms.DefaultSetConfig()
			policy.Prefix, policy.ExtraForms, policy.MaxForms, policy.AbsoluteMax = "files", 1, 3, 3
			policy.CanDelete, policy.ValidateMin, policy.ValidateMax = true, true, true
			current := article.NewArticleWithID(11)
			current.Title, current.Summary = "old/first.txt", new("old/summary.txt")
			root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			}()
			for _, name := range []string{current.Title, *current.Summary} {
				if _, err := root.Save(t.Context(), name, strings.NewReader("old content"), storage.SaveOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			backend := &collectionFileStorage{Backend: root, mode: mode}
			var definition ModelCollectionFormSet[article.Article]
			var receipts []formmodel.SetFilePublication
			var retained []uploads.File
			calls, currentLoads := 0, 0
			definition, err = NewCollectionFormSet(CollectionFormSetConfig[article.Article]{
				Name: "files", Label: "Edit files", Project: binding, Source: ir.ModelIdentity{AppLabel: config.AppLabel, ModelName: metadata.Name}, Manager: manager, Permissions: config.Permissions,
				Form: FormConfig{Definition: formmodel.Definition{Fields: []string{"title", "summary"}}}, Set: policy,
				Load: func(context.Context, auth.Principal, CollectionSetAccess) (CollectionSetSnapshot[article.Article], error) {
					return CollectionSetSnapshot[article.Article]{Current: []article.Article{current}}, nil
				},
				Run: func(ctx context.Context, actor auth.Principal, submission CollectionSetSubmission) (CollectionSetResult, error) {
					calls++
					bound, err := definition.BindCurrent(ctx, actor, submission, func(context.Context, CollectionSetAccess) (CollectionSetCurrent[article.Article], error) {
						currentLoads++
						return CollectionSetCurrent[article.Article]{Rows: CollectionSetSnapshot[article.Article]{Current: []article.Article{current}}}, nil
					})
					if err != nil || !bound.Set().Valid() {
						t.Fatal("multipart current binding", err)
					}
					for _, row := range bound.Set().FormSet().Forms() {
						for _, field := range []string{"title", "summary"} {
							value, present := row.Form().Cleaned().File(field)
							file, uploaded := value.Upload()
							if !present || !uploaded {
								continue
							}
							if row.Form().ReadOnly() {
								t.Fatal("readonly row acquired an upload")
							}
							retained = append(retained, file)
							reader, err := file.Open(ctx)
							if err != nil {
								t.Fatal(err)
							}
							content, readErr := io.ReadAll(reader)
							if err := reader.Close(); err != nil {
								t.Fatal(err)
							}
							if readErr != nil || string(content) != "row attachment" {
								t.Fatal("row upload changed", readErr)
							}
						}
					}
					if mode == "current_rejection" {
						return CollectionSetResult{}, RejectCollectionFormSet(map[int]validation.Errors{0: validation.NewErrors(validation.New("title", "invalid"))}, validation.Errors{}, nil)
					}
					prepared, err := bound.Set().Prepare()
					if err != nil {
						t.Fatal(err)
					}
					resolved, publications, err := prepared.SaveFiles(ctx, func(row formmodel.PreparedSetRow[article.Article]) ([]formmodel.FileSaver[article.Article], error) {
						var savers []formmodel.FileSaver[article.Article]
						for _, field := range row.Prepared().PendingFiles() {
							savers = append(savers, formmodel.FileSaver[article.Article]{Field: field, Backend: backend, Name: func(_ article.Article, file uploads.File) (string, error) {
								return fmt.Sprintf("row-%d/%s", row.Index(), file.Name()), nil
							}})
						}
						return savers, nil
					})
					receipts = publications
					if err != nil {
						return CollectionSetResult{}, err
					}
					if mode == "late_writer_failure" {
						return CollectionSetResult{}, errors.New("private writer failure after publication")
					}
					plan, err := resolved.SavePlan()
					if err != nil {
						t.Fatal(err)
					}
					// Inspect the exact typed plan without claiming a database save.
					var writes []CollectionSetWrite
					for _, row := range plan.Rows() {
						kind, key := "create", int64(21)
						if row.Existing() {
							kind, key = "update", current.ID
						}
						if mode == "clear" && row.Model().Title != "" {
							t.Fatal("clear lost its model reference")
						}
						if mode != "clear" && !strings.HasPrefix(row.Model().Title, fmt.Sprintf("row-%d/", row.Index())) {
							t.Fatal("resolved plan lost actual publication name")
						}
						writes = append(writes, CollectionSetWrite{Index: row.Index(), Kind: kind, ID: key})
					}
					for _, row := range plan.Deleted() {
						writes = append(writes, CollectionSetWrite{Index: row.Index(), Kind: "delete", ID: current.ID})
					}
					return bound.Result(writes)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			config.CollectionFormSets = []CollectionFormSet{definition.View()}
			var authorizer auth.Authorizer = auth.PrincipalAuthorizer{}
			if mode == "readonly_and_create" {
				authorizer = siteDenyAuthorizer{denied: config.Permissions.Change}
			}
			if mode == "denied_add" {
				authorizer = siteDenyAuthorizer{denied: config.Permissions.Add}
			}
			uploadPolicy := uploadTestPolicy(t)
			client, _, _ := uploadTestSite(t, config, uploadPolicy, authorizer)
			client.login(t, "admin", "secret", "/admin/")
			path := "/admin/articles/collection-set/files/"
			page := client.do(http.MethodGet, path, nil)
			if page.Code != 200 || !strings.Contains(page.Body.String(), `enctype="multipart/form-data"`) || !strings.Contains(page.Body.String(), "old/first.txt") {
				t.Fatal("file collection initial display", page.Code, page.Body.String())
			}
			values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "files-TOTAL_FORMS": {"2"}, "files-INITIAL_FORMS": {"1"}, "files-0-id": {"11"}, "files-1-id": {""}}
			parts := []siteUploadPart{{"files-0-title", "first.txt", "row attachment"}, {"files-1-title", "second.txt", "row attachment"}}
			status, wantCalls, wantSaves := 302, 1, 2
			switch mode {
			case "readonly_and_create":
				wantSaves = 1
			case "preserve":
				parts, wantSaves = nil, 0
			case "clear":
				parts, wantSaves = nil, 0
				values.Set("files-0-title-clear", "on")
			case "deleted_upload":
				parts, wantSaves = parts[:1], 0
				values.Set("files-0-DELETE", "on")
			case "contradiction":
				values.Set("files-0-title-clear", "on")
				status, wantCalls, wantSaves = 200, 0, 0
			case "duplicate_file":
				parts[1].field = "files-0-title"
				status, wantCalls, wantSaves = 200, 0, 0
			case "unknown_file", "primary_file", "noncanonical_row":
				parts[0].field = map[string]string{"unknown_file": "files-0-unknown", "primary_file": "files-0-id", "noncanonical_row": "files-00-title"}[mode]
				status, wantCalls, wantSaves = 400, 0, 0
			case "denied_add":
				status, wantCalls, wantSaves = 403, 0, 0
			case "current_rejection":
				status, wantSaves = 200, 0
			case "late_writer_failure", "partial_publish", "unknown_publish":
				status = 500
			}
			response := siteUploadSubmit(t, client, path, values, parts...)
			if response.Code != status || calls != wantCalls || currentLoads != wantCalls || backend.saves != wantSaves || backend.deletes != 0 {
				t.Fatal("multipart/publication boundary", response.Code, status, calls, currentLoads, backend.saves, response.Body.String())
			}
			if status == 200 && (!strings.Contains(response.Body.String(), `data-file-reselect="files-0-title"`) || strings.Contains(response.Body.String(), `value="first.txt"`)) {
				t.Fatal("invalid file display did not request reselection")
			}
			if len(receipts) != wantSaves {
				t.Fatal("attempted file receipts lost", len(receipts), wantSaves)
			}
			for index, receipt := range receipts {
				wantOutcome := storage.Published
				if index == 1 && mode == "partial_publish" {
					wantOutcome = storage.NotPublished
				}
				if index == 1 && mode == "unknown_publish" {
					wantOutcome = storage.Uncertain
				}
				if receipt.File().Outcome() != wantOutcome || receipt.File().Field() != "title" {
					t.Fatal("publication outcome was flattened")
				}
				if wantOutcome != storage.NotPublished {
					reader, err := root.Open(t.Context(), receipt.File().Info().Name())
					if err != nil {
						t.Fatal("published content was removed after failure", err)
					}
					content, readErr := io.ReadAll(reader)
					if err := reader.Close(); err != nil {
						t.Fatal(err)
					}
					if readErr != nil || string(content) != "row attachment" {
						t.Fatal("stored publication differs", readErr)
					}
				}
			}
			for _, name := range []string{current.Title, *current.Summary} {
				if _, err := root.Stat(t.Context(), name); err != nil {
					t.Fatal("old reference was automatically deleted", err)
				}
			}
			for _, file := range retained {
				if _, err := file.Open(context.Background()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
					t.Fatal("file capability survived request", err)
				}
			}
			assertUploadTempEmpty(t, uploadPolicy.TempDir)
		})
	}
}
