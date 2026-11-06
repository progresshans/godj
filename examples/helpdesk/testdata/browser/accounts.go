package main

import (
	"context"
	"errors"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/accounts"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/query"
)

// The first account initializes the domain but is never used by the browser.
// The operator is a real staff account with explicit grants, not a superuser.
func provisionBrowserAccounts(ctx context.Context, backend helpdesk.SelectedBackend, hasher auth.PasswordHasher) (*accounts.Runtime, auth.Principal, error) {
	var nobody auth.Principal
	firstConfig, err := accounts.FirstAccountConfig()
	if err != nil {
		return nil, nobody, err
	}
	first, err := project.NewFirstAccount(backend, hasher, firstConfig)
	if err != nil {
		return nil, nobody, err
	}
	input, err := first.Manager().Input(map[string]query.Value{"username": query.String("browser-bootstrap"), "email": query.String("")})
	if err != nil {
		return nil, nobody, err
	}
	const bootstrapPassword = "Quartz-river-fjord-72!"
	pending, err := first.Prepare(ctx, input, bootstrapPassword)
	if err != nil {
		return nil, nobody, err
	}
	initial, err := first.Commit(ctx, pending)
	if err != nil {
		return nil, nobody, err
	}
	config, err := accounts.Policy()
	if err != nil {
		return nil, nobody, err
	}
	runtime, err := project.NewAccountRuntime(ctx, backend, hasher, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		return nil, nobody, err
	}
	startup, err := runtime.InspectStartup(ctx)
	if err != nil {
		return nil, nobody, err
	}
	if !startup.Ready() || startup.Ownership().OperationID() != initial.Ownership.OperationID() || startup.SessionRecords() != 0 || startup.AuditEvents() != 1 {
		return nil, nobody, errors.New("browser selected-account startup differs from first-account receipt")
	}
	bootstrap, err := runtime.Authenticator().Authenticate(ctx, "browser-bootstrap", bootstrapPassword)
	if err != nil {
		return nil, nobody, err
	}
	permissions := append([]auth.Permission{"godj.admin.access"}, helpdesk.Permissions()...)
	var keys []int64
	catalog := runtime.Catalog().Permissions()
	for _, permission := range permissions {
		input, err := catalog.Manager().Input(map[string]query.Value{"code": query.String(string(permission)), "name": query.String(string(permission))})
		if err != nil {
			return nil, nobody, err
		}
		created, err := catalog.Create(ctx, bootstrap.Principal(), input)
		if err != nil {
			return nil, nobody, err
		}
		keys = append(keys, created.ID)
	}
	creator := runtime.Accounts().Creator()
	input, err = creator.Manager().Input(map[string]query.Value{
		"username": query.String("operator"), "email": query.String(""),
		"active": query.Boolean(true), "staff": query.Boolean(true), "superuser": query.Boolean(false),
	})
	if err != nil {
		return nil, nobody, err
	}
	input, err = input.WithCollections(map[string][]int64{"permissions": keys})
	if err != nil {
		return nil, nobody, err
	}
	prepared, err := creator.Prepare(ctx, bootstrap.Principal(), input, "demo-password")
	if err != nil {
		return nil, nobody, err
	}
	if _, err := creator.Commit(ctx, bootstrap.Principal(), prepared); err != nil {
		return nil, nobody, err
	}
	credential, err := runtime.Authenticator().Authenticate(ctx, "operator", "demo-password")
	if err != nil {
		return nil, nobody, err
	}
	actor := credential.Principal()
	actual := actor.Permissions()
	slices.Sort(actual)
	slices.Sort(permissions)
	if _, bound := actor.CredentialDomain(); !bound || !actor.Staff() || actor.Superuser() || !slices.Equal(actual, permissions) {
		return nil, nobody, errors.New("browser operator lacks the selected domain or explicit staff grants")
	}
	return runtime, actor, nil
}

type browserReadBackend interface {
	db.Queryer
	db.SnapshotReader
}

type browserAuditEntry struct {
	Sequence      int64        `json:"sequence"`
	Domain        string       `json:"domain"`
	ActorID       string       `json:"actor_id"`
	Model         string       `json:"model"`
	ObjectID      int64        `json:"object_id"`
	Action        admin.Action `json:"action"`
	ChangedFields []string     `json:"changed_fields"`
	At            time.Time    `json:"at"`
}

func browserAuditHistory(ctx context.Context, backend db.SnapshotReader, actor auth.Principal, model string, id int64, limit int) ([]browserAuditEntry, error) {
	namespace, bound := actor.CredentialDomain()
	if !bound {
		return nil, errors.New("browser audit actor has no selected domain")
	}
	return db.ReadSnapshotResult(ctx, backend, func(work context.Context, reader db.Queryer) ([]browserAuditEntry, error) {
		entries, err := accountaudit.HistoryIn(work, reader, namespace, model, id, limit)
		if err != nil {
			return nil, err
		}
		result := make([]browserAuditEntry, len(entries))
		for index, entry := range entries {
			if entry.Domain() != namespace || entry.ActorID() != actor.ID() {
				return nil, errors.New("browser business audit crossed actor or domain")
			}
			result[index] = browserAuditEntry{entry.Sequence(), entry.Domain().Identifier(), entry.ActorID(), entry.Model(), entry.ObjectID(), entry.Action(), entry.ChangedFields(), entry.At()}
		}
		return result, nil
	})
}

func browserAccountEvidence(ctx context.Context, runtime *accounts.Runtime, actor auth.Principal) (map[string]any, error) {
	current, err := runtime.Authenticator().Resolve(ctx, actor.ID())
	if err != nil {
		return nil, err
	}
	namespace, bound := current.Principal().CredentialDomain()
	if !bound || !namespace.MatchesPrincipal(actor) || !current.Principal().Staff() || current.Principal().Superuser() {
		return nil, errors.New("browser selected-account authority changed")
	}
	startup, err := runtime.InspectStartup(ctx)
	if err != nil {
		return nil, err
	}
	if !startup.Ready() || startup.SessionRecords() < 1 {
		return nil, errors.New("browser did not retain a native authenticated session")
	}
	binding, err := project.BindAccount()
	if err != nil {
		return nil, err
	}
	role, err := binding.Role()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"model":  role.Model.AppLabel + "." + role.Model.ModelName,
		"domain": namespace.Identifier(), "role_fingerprint": startup.Ownership().RoleFingerprint(),
		"actor_id": actor.ID(), "active": current.Principal().Active(),
		"staff": current.Principal().Staff(), "superuser": current.Principal().Superuser(),
		"permissions": current.Principal().Permissions(), "session_records": startup.SessionRecords(),
	}, nil
}
