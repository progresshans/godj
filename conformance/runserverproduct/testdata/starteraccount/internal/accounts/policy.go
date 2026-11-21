package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	access "example.com/started-site/apps/access/models"
	people "example.com/started-site/apps/people/models"
	"example.com/started-site/internal/hostprofile"
	"example.com/started-site/project"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	formmodel "github.com/progresshans/godj/forms/model"
	identityadmin "github.com/progresshans/godj/identity/admin"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
)

type Runtime = accountmodel.Runtime[people.Member, access.Team, access.Grant]

func OpenAPIAccess() (api.PermissionPolicy, error) {
	return api.NewPermissionPolicy([]auth.Permission{"people.read_schema"}, nil)
}

func Policy() (accountmodel.RuntimeConfig[people.Member, access.Team, access.Grant], error) {
	var config accountmodel.RuntimeConfig[people.Member, access.Team, access.Grant]
	binding, err := project.BindAccount()
	if err != nil {
		return config, err
	}
	passwords, err := binding.DefaultPasswordValidator(accountmodel.PasswordAttribute{Field: "contact", Label: "contact"})
	if err != nil {
		return config, err
	}
	member := accountmodel.AccountPermissions{View: "people.view_member", Add: "people.add_member", Change: "people.change_member", Delete: "people.delete_member"}
	team := accountmodel.CatalogPermissions{View: "access.view_team", Add: "access.add_team", Change: "access.change_team", Delete: "access.delete_team"}
	grant := accountmodel.CatalogPermissions{View: "access.view_grant", Add: "access.add_grant", Change: "access.change_grant", Delete: "access.delete_grant"}
	fields := []string{"contact", "region", "enabled", "root", "teams", "grants"}
	var files []accountmodel.ProfileFile[people.Member]
	if hostprofile.Document {
		document, err := accountmodel.NewProfileFile(binding, "document", accountmodel.ProfileFileConfig[people.Member]{
			Backend: documentStore{os.Getenv("STARTER_MEDIA_DIR")},
			Name:    func(_ people.Member, file uploads.File) (string, error) { return "documents/" + file.Name(), nil },
		})
		if err != nil {
			return config, err
		}
		fields = append(fields, "document")
		files = append(files, document)
	}
	config.Accounts = accountmodel.AccountManagementConfig[people.Member]{
		ReadFields: append([]string{"seen"}, fields...), CreateFields: fields, EditFields: fields,
		Files:       files,
		Permissions: member, MaxPasswordBytes: 1024, ValidatePassword: passwords,
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: team.Delete, Change: team.Change},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: grant.Delete, Change: grant.Change},
	}
	config.Catalog = accountmodel.CatalogConfig[access.Team, access.Grant]{
		Group:              accountmodel.CatalogModelConfig[access.Team]{ReadFields: []string{"title", "priority", "grants"}, WriteFields: []string{"title", "grants"}, Permissions: team},
		Permission:         accountmodel.CatalogModelConfig[access.Grant]{ReadFields: []string{"key", "title", "description"}, WriteFields: []string{"key", "title"}, Permissions: grant},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: member.Delete, Change: member.Change},
	}
	config.SessionStorage.MaxRecords = 256
	return config, nil
}

func firstAccountConfig(management accountmodel.AccountManagementConfig[people.Member]) (accountmodel.FirstAccountConfig[people.Member], firstaccount.Config) {
	fields := []string{"contact", "region"}
	var command firstaccount.Config
	if hostprofile.Document {
		fields = append(fields, "document")
		command = firstaccount.Config{MaxFileBytes: 128, MaxFilesBytes: 128, ReportFiles: func(_ context.Context, report firstaccount.FileReport) error {
			account := report.Creation().Account
			return recordDocument("first", report.OperationID(), account.ID, account.Revision, report.Files(), report.Err())
		}}
	}
	return accountmodel.FirstAccountConfig[people.Member]{CreationPolicy: accountmodel.CreationPolicy[people.Member]{
		Fields: fields, Files: management.Files, MaxPasswordBytes: management.MaxPasswordBytes,
		ValidatePassword: management.ValidatePassword, Validate: management.ValidateCreate,
		CaseInsensitiveLogin: management.CaseInsensitiveCreationLogin,
		PrincipalID:          management.PrincipalID, Now: management.Now,
	}}, command
}

func AdminAccountRegistration(listFields []string) identityadmin.AccountRegistration {
	presentation := identityadmin.AccountRegistration{Slug: "users", ListFields: listFields}
	if hostprofile.Document {
		presentation.ReportFileCreate = func(_ context.Context, r identityadmin.AccountFileCreationReport) error {
			return recordDocument("create", "", r.Creation().ID, r.Creation().Revision, r.Publications(), r.Err())
		}
		presentation.ReportFileEdit = func(_ context.Context, r identityadmin.AccountFileEditReport) error {
			return recordDocument("edit", "", r.ID(), r.Change().Revision, r.Publications(), r.Err())
		}
	}
	return presentation
}

// Description and configuration checks can run in a process without media.
// Each requested write acquires and releases its own filesystem handle.
type documentStore struct{ directory string }

func (store documentStore) Save(ctx context.Context, name string, r io.Reader, options storage.SaveOptions) (storage.Info, error) {
	disk, err := storage.OpenFilesystem(ctx, storage.FilesystemConfig{Directory: store.directory})
	if err != nil {
		return storage.Info{}, err
	}
	info, err := disk.Save(ctx, name, r, options)
	if cleanup := disk.Close(); cleanup != nil {
		outcome := storage.Uncertain
		var failed *storage.Error
		if info.Valid() {
			outcome = storage.Published
		} else if errors.As(err, &failed) {
			outcome = failed.Outcome
		}
		err = &storage.Error{Code: "close_failed", Outcome: outcome, Cause: errors.Join(err, cleanup)}
	}
	return info, err
}

func (documentStore) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, os.ErrPermission
}
func (documentStore) Stat(context.Context, string) (storage.Info, error) {
	return storage.Info{}, os.ErrPermission
}
func (documentStore) Delete(context.Context, string) error { return os.ErrPermission }

func recordDocument(action, operation string, id, revision int64, publications []formmodel.FilePublication, failure error) error {
	if len(publications) != 1 {
		return errors.New("expected one document publication")
	}
	publication := publications[0]
	record := struct {
		Operation, Field, Name    string
		ID, Revision, Size        int64
		Outcome                   storage.Outcome
		AccountFailed, FileFailed bool
	}{operation, publication.Field(), publication.Info().Name(), id, revision, publication.Info().Size(), publication.Outcome(), failure != nil, publication.Err() != nil}
	// Preserve the first report for each action. A second edit exercises a
	// real reporting failure after the account and storage have committed.
	file, err := os.OpenFile(filepath.Join(os.Getenv("STARTER_REPORTS_DIR"), action+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(file).Encode(record), file.Close())
}
