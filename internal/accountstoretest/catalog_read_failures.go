package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
)

type catalogReadBackend struct {
	Backend
	read snapshotFunc
}

func (owner *catalogReadBackend) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	return owner.read(ctx, callback)
}

type catalogReadProbe struct {
	db.Queryer
	validator db.SessionValidator
	fault     error
	closeFail bool
	table     string
	skip      int
	matches   int
}

func (probe *catalogReadProbe) ValidateSession(ctx context.Context) error {
	return probe.validator.ValidateSession(ctx)
}
func (probe *catalogReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	matched := plan.Table() == probe.table
	if matched {
		probe.matches++
	}
	fail := probe.fault != nil && matched && probe.matches > probe.skip
	if fail && !probe.closeFail {
		return nil, probe.fault
	}
	rows, err := probe.Queryer.Query(ctx, plan)
	if rows != nil && fail && probe.closeFail {
		return &profileReadFaultRows{Rows: rows, fault: probe.fault}, err
	}
	return rows, err
}

func catalogFailedRead[M any](t *testing.T, ctx context.Context, resource accountmodel.CatalogResource[M], actor auth.Principal, id int64, operation string) error {
	t.Helper()
	switch operation {
	case "get":
		value, err := resource.Get(ctx, actor, id)
		if value.Revision() != 0 || value.View().Values() != nil {
			t.Fatal("failed catalog read retained selected data")
		}
		return err
	case "list":
		value, err := resource.List(ctx, actor, 0, 100)
		if value.Items != nil || value.Total != 0 {
			t.Fatal("failed catalog list retained page")
		}
		return err
	default:
		value, err := resource.History(ctx, actor, id, 100)
		if value != nil {
			t.Fatal("failed catalog history retained entries")
		}
		return err
	}
}

func catalogReadFailures(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	group, err := f.catalog.Groups().Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("read failure target")}, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "repeated", "query", "close", "swallowed", "cleanup", "cancel", "denial_cleanup", "missing_cleanup"} {
		t.Run(mode, func(t *testing.T) {
			for _, operation := range []string{"get", "list", "history"} {
				if mode == "missing_cleanup" && operation == "list" {
					continue
				}
				t.Run(operation, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					fault := errors.New("private catalog read cleanup details")
					owner := &catalogReadBackend{Backend: backend}
					owner.read = func(ctx context.Context, observe func(db.Queryer) error) error {
						switch mode {
						case "skipped":
							return nil
						case "nil":
							return observe(nil)
						case "typed_nil":
							return observe((*catalogReadProbe)(nil))
						case "root":
							return observe(backend)
						}
						err := backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
							// Fail the catalog target/history query after current authority
							// has completed, so partial selected results cannot escape.
							probe := &catalogReadProbe{Queryer: scope, validator: scope.(db.SessionValidator), table: "godj_identity_group", skip: 1}
							if operation == "history" {
								probe.table = "godj_account_audit_event"
								probe.skip = 0
							}
							if mode == "query" || mode == "close" || mode == "swallowed" {
								probe.fault, probe.closeFail = fault, mode == "close"
							}
							err := observe(probe)
							if mode == "swallowed" {
								return nil
							}
							if mode == "repeated" {
								err = errors.Join(err, observe(probe))
							}
							return err
						})
						if mode == "cleanup" || mode == "denial_cleanup" || mode == "missing_cleanup" {
							return errors.Join(err, fault)
						}
						if mode == "cancel" {
							cancel()
						}
						return err
					}
					config := catalogConfig()
					if mode == "denial_cleanup" {
						config.Group.Permissions.View, config.Group.Permissions.Change = "catalog.absent_view", "catalog.absent_change"
					}
					catalog, err := project.NewAccountCatalog(catalogSessions(t, owner), auth.PrincipalAuthorizer{}, config)
					if err != nil {
						t.Fatal(err)
					}
					id := group.ID
					if mode == "missing_cleanup" {
						id = mathMaxKey
					}
					err = catalogFailedRead(t, ctx, catalog.Groups(), actor, id, operation)
					requireProfileCode(t, err, accountmodel.CodePersistence)
					if strings.Contains(fmt.Sprintf("%+v", err), "private") {
						t.Fatal("catalog read disclosed private cause")
					}
					if mode == "query" || mode == "close" || mode == "swallowed" || strings.HasSuffix(mode, "cleanup") {
						if !errors.Is(err, fault) {
							t.Fatal("catalog read lost cause", err)
						}
					}
					if mode == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("catalog read lost cancellation")
					}
				})
			}
		})
	}
	// A missing permission must also retain a clean zero result; the typed
	// resource does not turn an absent row into its zero-valued model.
	for _, operation := range []string{"get", "history"} {
		err := catalogFailedRead[models.Permission](t, t.Context(), f.catalog.Permissions(), actor, mathMaxKey, operation)
		requireProfileCode(t, err, accountmodel.CodeNotFound)
	}
}
