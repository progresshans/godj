package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	sessiondb "github.com/progresshans/godj/sessions/database"
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
	ready     func() bool
}

func (probe *catalogReadProbe) ValidateSession(ctx context.Context) error {
	return probe.validator.ValidateSession(ctx)
}
func (probe *catalogReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	matched := plan.Table() == probe.table && (probe.ready == nil || probe.ready())
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
		if _, loaded := value.View().Collection("permissions"); value.ID() != 0 || value.Revision() != 0 || value.View().Values() != nil || loaded {
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

func catalogReadFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalogOperation(t, backend)
	t.Run("group", func(t *testing.T) {
		catalogResourceReadFailures(t, backend, reopen, f, "group", f.group.ID, func(catalog *accountmodel.Catalog[models.Group, models.Permission]) accountmodel.CatalogResource[models.Group] {
			return catalog.Groups()
		})
	})
	t.Run("permission", func(t *testing.T) {
		catalogResourceReadFailures(t, backend, reopen, f, "permission", f.permission.ID, func(catalog *accountmodel.Catalog[models.Group, models.Permission]) accountmodel.CatalogResource[models.Permission] {
			return catalog.Permissions()
		})
	})
}

func catalogResourceReadFailures[M any](t *testing.T, backend Backend, reopen func(*testing.T) Backend, f *catalogOperationFixture, kind string, target int64, selectResource func(*accountmodel.Catalog[models.Group, models.Permission]) accountmodel.CatalogResource[M]) {
	actor := f.credential.Principal()
	resource := selectResource(f.catalog)
	if !t.Run("baseline", func(t *testing.T) {
		before := catalogHostSnapshot(t, reopen(t))
		detail, err := resource.Get(t.Context(), actor, target)
		if err != nil || detail.ID() != target || detail.Revision() != 1 {
			t.Fatal("catalog read failure control did not load its target", err)
		}
		page, err := resource.List(t.Context(), actor, 0, 100)
		if err != nil || page.Total == 0 || len(page.Items) == 0 {
			t.Fatal("catalog read failure control did not load its page", err)
		}
		history, err := resource.History(t.Context(), actor, target, 100)
		if err != nil || len(history) != 1 {
			t.Fatal("catalog read failure control did not load its actual audit", err)
		}
		if !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
			t.Fatal("catalog read control changed native state")
		}
	}) {
		return
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
					calls, authorized := 0, false
					var probe *catalogReadProbe
					owner.read = func(ctx context.Context, observe func(db.Queryer) error) error {
						calls++
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
							// Arm only after actual current authority succeeds. Group and
							// Permission use different numbers of credential lookup queries.
							probe = &catalogReadProbe{Queryer: scope, validator: scope.(db.SessionValidator), table: "godj_identity_" + kind, ready: func() bool { return authorized }}
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
						policy := &config.Group.Permissions
						if kind == "permission" {
							policy = &config.Permission.Permissions
						}
						policy.View, policy.Change = "catalog.absent_view", "catalog.absent_change"
					}
					overlay := overlayFunc(func(ctx context.Context, current auth.Principal, permission auth.Permission) (bool, error) {
						allowed, err := (auth.PrincipalAuthorizer{}).Allowed(ctx, current, permission)
						authorized = allowed && err == nil
						return allowed, err
					})
					store, err := hostproject.NewAccountSessionStore(owner, sessiondb.Config{})
					if err != nil {
						t.Fatal(err)
					}
					catalog, err := hostproject.NewAccountCatalog(store, overlay, config)
					if err != nil {
						t.Fatal(err)
					}
					id := target
					if mode == "missing_cleanup" {
						id = mathMaxKey
					}
					before := catalogHostSnapshot(t, reopen(t))
					if mode == "denial_cleanup" {
						controlStore, err := hostproject.NewAccountSessionStore(backend, sessiondb.Config{})
						if err != nil {
							t.Fatal(err)
						}
						control, err := hostproject.NewAccountCatalog(controlStore, auth.PrincipalAuthorizer{}, config)
						if err != nil {
							t.Fatal(err)
						}
						requireProfileCode(t, catalogFailedRead(t, t.Context(), selectResource(control), actor, id, operation), accountmodel.CodePermission)
					}
					err = catalogFailedRead(t, ctx, selectResource(catalog), actor, id, operation)
					if calls != 1 {
						t.Fatal("catalog read retried its snapshot owner", calls)
					}
					if mode == "query" || mode == "close" || mode == "swallowed" {
						if !authorized || probe == nil || probe.matches != 1 {
							t.Fatal("catalog read did not reach its target after current authority")
						}
					}
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
					if !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
						t.Fatal("failed catalog read changed native state")
					}
				})
			}
		})
	}
	t.Run("not_found", func(t *testing.T) {
		for _, operation := range []string{"get", "history"} {
			t.Run(operation, func(t *testing.T) {
				before := catalogHostSnapshot(t, reopen(t))
				err := catalogFailedRead(t, t.Context(), resource, actor, mathMaxKey, operation)
				requireProfileCode(t, err, accountmodel.CodeNotFound)
				if !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
					t.Fatal("absent catalog read changed native state")
				}
			})
		}
	})
}
