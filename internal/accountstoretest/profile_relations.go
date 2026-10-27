package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func runProfileRelations(t *testing.T, open Open, storedKey StoredKeyFixture) {
	t.Helper()
	t.Run("profile_relations", func(t *testing.T) {
		for _, test := range []struct {
			name string
			run  func(*testing.T, Backend, func(*testing.T) Backend)
		}{{"lifecycle", profileRelationLifecycle}, {"prepare_recheck", profileRelationRecheck}, {"failures", profileRelationFailures},
			{"signed_keys", func(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
				profileRelationSignedKeys(t, backend, reopen, storedKey)
			}}} {
			t.Run(test.name, func(t *testing.T) {
				backend, reopen := open(t)
				migrateDeletion(t, backend)
				test.run(t, backend, reopen)
			})
		}
	})
}

func sponsorRelation(t *testing.T, binding accountmodel.Binding[people.Member]) accountmodel.ProfileRelation[people.Member] {
	t.Helper()
	relation, err := accountmodel.NewProfileRelation(binding, "sponsor", access.TeamObjects, accountmodel.ProfileRelationConfig[access.Team]{
		Choices: formmodel.ModelChoiceConfig[access.Team]{Label: func(row access.Team) (string, error) { return row.Title, nil },
			Search: func(term string) ([]orm.Predicate[access.Team], error) {
				return []orm.Predicate[access.Team]{access.TeamFields.Title.IContains(term)}, nil
			}},
		Filter: func(ctx context.Context, reader db.Queryer, actor auth.Principal, origin accountmodel.ProfileRelationOrigin) ([]orm.Predicate[access.Team], error) {
			if _, native := reader.(db.SessionValidator); !native || ctx.Err() != nil {
				return nil, errors.New("relation policy lost its borrowed native scope")
			}
			if origin.Operation() != accountmodel.ProfileRelationFirstAccount && !actor.Authenticated() {
				return nil, errors.New("relation policy received stale admission")
			}
			return []orm.Predicate[access.Team]{access.TeamFields.Priority.GreaterThan(0)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return relation
}

func sponsorGroup(t *testing.T, backend db.Session, name string, priority int64) access.Team {
	t.Helper()
	row, err := access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate(name).WithPriority(priority))
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func relationInput(t *testing.T, values map[string]query.Value) orm.ModelInput[people.Member] {
	t.Helper()
	input, err := people.MemberObjects.Input(values)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func relationProfileConfig(relation accountmodel.ProfileRelation[people.Member]) accountmodel.ProfileConfig[people.Member] {
	return accountmodel.ProfileConfig[people.Member]{Fields: []string{"region", "sponsor"}, Relations: []accountmodel.ProfileRelation[people.Member]{relation}, ChangePermission: "account.change", Now: func() time.Time { return joined }}
}

func relationMember(t *testing.T, backend db.Queryer, id int64) people.Member {
	t.Helper()
	row, err := people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(id)).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func relationHistory(t *testing.T, backend Backend, actor auth.Principal, id int64) []accountaudit.Entry {
	t.Helper()
	namespace, _ := actor.CredentialDomain()
	var entries []accountaudit.Entry
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
		var err error
		entries, err = accountaudit.HistoryIn(t.Context(), reader, namespace, "people.member", id, 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return entries
}

func assertDeletionSnapshot(t *testing.T, before, after map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		for name, expected := range before {
			if !reflect.DeepEqual(expected, after[name]) {
				t.Errorf("relation rejection changed durable %s state", name)
			}
		}
		t.FailNow()
	}
}

func profileRelationLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	target := deletionMember(t, backend, f, "profile", nil)
	group := sponsorGroup(t, backend, "Allowed sponsor", 1)
	relation := sponsorRelation(t, f.binding)
	config := relationProfileConfig(relation)
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Relations[0] = accountmodel.ProfileRelation[people.Member]{}
	input := relationInput(t, map[string]query.Value{"sponsor": query.Integer(group.ID), "region": query.String("NZ")})
	changed, err := editor.Update(t.Context(), f.actor, target.ID, 1, input)
	if err != nil || changed.Revision != 2 || !slices.Equal(changed.ChangedFields, []string{"region", "sponsor"}) {
		t.Fatal("relation profile did not commit", changed, err)
	}
	want := target
	want.Region, want.SponsorID, want.Version = "NZ", &group.ID, 2
	if !reflect.DeepEqual(relationMember(t, reopen(t), target.ID), want) {
		t.Fatal("relation write altered protected or omitted account fields")
	}
	if entries := relationHistory(t, backend, f.actor, target.ID); len(entries) != 1 || !slices.Equal(entries[0].ChangedFields(), changed.ChangedFields) {
		t.Fatal("relation audit differs")
	}
	before := deletionSnapshot(t, backend)
	noop, err := editor.Update(t.Context(), f.actor, target.ID, 2, input)
	if err != nil || noop.Revision != 2 || len(noop.ChangedFields) != 0 {
		t.Fatal("relation no-op", err)
	}
	assertDeletionSnapshot(t, before, deletionSnapshot(t, reopen(t)))
	reader, err := project.NewAccountProfileReader(backend, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{Fields: []string{"region", "sponsor"}, ViewPermission: "account.view"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := reader.Get(t.Context(), f.actor, target.ID)
	value, present := view.View().Get("sponsor")
	if err != nil || !present || !value.Equal(query.Integer(group.ID)) {
		t.Fatal("selected relation read lost key", err)
	}
	cleared, err := editor.Update(t.Context(), f.actor, target.ID, 2, relationInput(t, map[string]query.Value{"sponsor": query.Null()}))
	if err != nil || cleared.Revision != 3 || !slices.Equal(cleared.ChangedFields, []string{"sponsor"}) {
		t.Fatal("nullable relation clear", err)
	}
	want.SponsorID, want.Version = nil, 3
	if !reflect.DeepEqual(relationMember(t, reopen(t), target.ID), want) {
		t.Fatal("clear lost untouched fields")
	}
	// The combined owner commits profile FK, ordinary profile, memberships and
	// deactivation/session effects together, with one revision and one audit.
	record := deletionSession(t, f.manager, target.Subject)
	permissions := deletionConfig()
	combined, err := project.NewAccountEditor(f.store, auth.PrincipalAuthorizer{}, accountmodel.AccountEditConfig[people.Member]{
		Fields: []string{"region", "sponsor", "enabled", "teams"}, Relations: []accountmodel.ProfileRelation[people.Member]{relation}, ChangePermission: "account.change",
		RelatedPermissions: accountmodel.RelatedRowPermissions{Account: permissions.Account, Group: permissions.Group, Permission: permissions.Permission}, Now: func() time.Time { return joined },
	})
	if err != nil {
		t.Fatal(err)
	}
	combinedInput, err := relationInput(t, map[string]query.Value{"sponsor": query.Integer(group.ID), "region": query.String("JP"), "enabled": query.Boolean(false)}).WithCollections(map[string][]int64{"teams": {group.ID}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := combined.Update(t.Context(), f.actor, target.ID, 3, combinedInput)
	if err != nil || result.Revision != 4 || result.RevokedSessions != 1 {
		t.Fatal("combined relation/lifecycle commit", result, err)
	}
	want.SponsorID, want.Region, want.Enabled, want.Version = &group.ID, "JP", false, 4
	if !reflect.DeepEqual(relationMember(t, reopen(t), target.ID), want) {
		t.Fatal("combined relation/lifecycle durable state")
	}
	accountSessionRecord(t, f.store, record, false)
	entries := relationHistory(t, backend, f.actor, target.ID)
	if len(entries) != 3 || !slices.Equal(entries[2].ChangedFields(), result.ChangedFields) {
		t.Fatal("combined relation audit was split or lost")
	}
}

func profileRelationRecheck(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	group := sponsorGroup(t, backend, "Rechecked sponsor", 1)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	creator, err := project.NewAccountCreator(backend, auth.PrincipalAuthorizer{}, hasher, accountmodel.CreationConfig[people.Member]{
		CreationPolicy: accountmodel.CreationPolicy[people.Member]{Fields: []string{"contact", "region", "sponsor"}, Relations: []accountmodel.ProfileRelation[people.Member]{sponsorRelation(t, f.binding)}},
		AddPermission:  "account.add", ChangePermission: "account.change",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := relationInput(t, map[string]query.Value{"contact": query.String("related@example.test"), "region": query.String("NZ"), "sponsor": query.Integer(group.ID)})
	pending, err := creator.Prepare(t.Context(), f.actor, input, password)
	if err != nil {
		t.Fatal(err)
	}
	group, err = access.TeamObjects.Patch(t.Context(), reopen(t), group, access.TeamPatch{}.WithPriority(0))
	if err != nil {
		t.Fatal(err)
	}
	before := deletionSnapshot(t, backend)
	result, err := creator.Commit(t.Context(), f.actor, pending)
	diagnostics, rejected := validation.Rejected(err)
	if !rejected || diagnostics.ByField("sponsor").Empty() || result.Revision != 0 {
		t.Fatal("prepared relation became a lasting grant", result, err)
	}
	assertDeletionSnapshot(t, before, deletionSnapshot(t, reopen(t)))
	if _, err := creator.Commit(t.Context(), f.actor, pending); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
		t.Fatal("failed relation candidate retried", err)
	}
	group, err = access.TeamObjects.Patch(t.Context(), backend, group, access.TeamPatch{}.WithPriority(1))
	if err != nil {
		t.Fatal(err)
	}
	pending, err = creator.Prepare(t.Context(), f.actor, input, password)
	if err != nil {
		t.Fatal(err)
	}
	result, err = creator.Commit(t.Context(), f.actor, pending)
	if err != nil || result.Revision != 1 {
		t.Fatal("authorized relation creation", result, err)
	}
	row := relationMember(t, reopen(t), result.ID)
	if row.SponsorID == nil || *row.SponsorID != group.ID || row.Region != "NZ" || row.Root || row.Subject == f.actorRow.Subject || row.Secret == "" {
		t.Fatal("created relation account lost identity/policy")
	}
	// A no-op still checks the current target choice, then current actor
	// admission. A retained stale principal cannot restore its own authority.
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, relationProfileConfig(sponsorRelation(t, f.binding)))
	if err != nil {
		t.Fatal(err)
	}
	group, err = access.TeamObjects.Patch(t.Context(), backend, group, access.TeamPatch{}.WithPriority(0))
	if err != nil {
		t.Fatal(err)
	}
	before = deletionSnapshot(t, backend)
	_, err = editor.Update(t.Context(), f.actor, row.ID, 1, relationInput(t, map[string]query.Value{"region": query.String("NZ")}))
	if failures, rejected := validation.Rejected(err); !rejected || failures.ByField("sponsor").Empty() {
		t.Fatal("no-op skipped current relation policy", err)
	}
	assertDeletionSnapshot(t, before, deletionSnapshot(t, reopen(t)))
	_, err = people.MemberObjects.Patch(t.Context(), backend, f.actorRow, people.MemberPatch{}.WithEnabled(false))
	if err != nil {
		t.Fatal(err)
	}
	before = deletionSnapshot(t, backend)
	_, err = editor.Update(t.Context(), f.actor, row.ID, 1, relationInput(t, map[string]query.Value{"sponsor": query.Null()}))
	if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
		t.Fatal("stale actor gained relation write", err)
	}
	assertDeletionSnapshot(t, before, deletionSnapshot(t, reopen(t)))
}

func profileRelationFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	group := sponsorGroup(t, backend, "Failure sponsor", 1)
	for _, mode := range []string{"missing", "scope_error", "invalid_predicate_null", "label_error", "canceled_policy", "audit_failure", "cleanup_failure", "unknown_commit", "stale"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			target := deletionMember(t, backend, f, "relation-"+mode, nil)
			fault := errors.New("private relation policy failure")
			owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault}
			relation := sponsorRelation(t, f.binding)
			if mode == "scope_error" || mode == "invalid_predicate_null" || mode == "label_error" || mode == "canceled_policy" {
				var err error
				relation, err = accountmodel.NewProfileRelation(f.binding, "sponsor", access.TeamObjects, accountmodel.ProfileRelationConfig[access.Team]{
					Choices: formmodel.ModelChoiceConfig[access.Team]{Label: func(row access.Team) (string, error) {
						if mode == "label_error" {
							return "", fault
						}
						return row.Title, nil
					}},
					Filter: func(context.Context, db.Queryer, auth.Principal, accountmodel.ProfileRelationOrigin) ([]orm.Predicate[access.Team], error) {
						switch mode {
						case "scope_error":
							return nil, fault
						case "invalid_predicate_null":
							field := orm.NewIntegerField[access.Team](ir.Field{Name: "foreign_scope", GoName: "ForeignScope", Column: "foreign_scope", Kind: ir.FieldInteger})
							return []orm.Predicate[access.Team]{field.Exact(1)}, nil
						case "canceled_policy":
							cancel()
						}
						return nil, nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			editor, err := project.NewAccountProfileEditor(owner, auth.PrincipalAuthorizer{}, relationProfileConfig(relation))
			if err != nil {
				t.Fatal(err)
			}
			key, revision := group.ID, int64(1)
			if mode == "missing" {
				key = mathMaxKey
			}
			if mode == "stale" {
				revision = 2
			}
			before := deletionSnapshot(t, backend)
			selection := query.Integer(key)
			if mode == "invalid_predicate_null" {
				selection = query.Null()
			}
			result, err := editor.Update(ctx, f.actor, target.ID, revision, relationInput(t, map[string]query.Value{"sponsor": selection, "region": query.String("NZ")}))
			if err == nil || result.Revision != 0 || owner.calls != 1 {
				t.Fatal("relation failure published/retried", result, err, owner.calls)
			}
			committed := mode == "unknown_commit" || mode == "cleanup_failure"
			if !committed {
				assertDeletionSnapshot(t, before, deletionSnapshot(t, reopen(t)))
			} else {
				current := relationMember(t, reopen(t), target.ID)
				want := target
				want.SponsorID, want.Region, want.Version = &group.ID, "NZ", 2
				if !reflect.DeepEqual(current, want) || len(relationHistory(t, backend, f.actor, target.ID)) != 1 {
					t.Fatal("uncertain response did not retain the actual committed relation/audit")
				}
			}
		})
	}
}

func profileRelationSignedKeys(t *testing.T, backend Backend, reopen func(*testing.T) Backend, storedKey StoredKeyFixture) {
	f := provisionDeletion(t, backend)
	for _, key := range []int64{0, -9} {
		group := sponsorGroup(t, backend, "Signed sponsor "+time.Unix(key, 0).String(), 1)
		target := deletionMember(t, backend, f, "signed-"+time.Unix(key, 0).Format("150405"), nil)
		storedKey(t, backend, "team", group.ID, key)
		storedKey(t, backend, "member", target.ID, key)
		relation, err := accountmodel.NewProfileRelation(f.binding, "sponsor", access.TeamObjects, accountmodel.ProfileRelationConfig[access.Team]{
			Choices: formmodel.ModelChoiceConfig[access.Team]{Label: func(row access.Team) (string, error) { return row.Title, nil }},
			Filter: func(_ context.Context, _ db.Queryer, actor auth.Principal, origin accountmodel.ProfileRelationOrigin) ([]orm.Predicate[access.Team], error) {
				id, present := origin.TargetID()
				if !actor.Authenticated() || !present || id != key || origin.Operation() != accountmodel.ProfileRelationChange {
					return nil, errors.New("signed target lost its relation policy origin")
				}
				return []orm.Predicate[access.Team]{access.TeamFields.ID.Exact(key)}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, relationProfileConfig(relation))
		if err != nil {
			t.Fatal(err)
		}
		result, err := editor.Update(t.Context(), f.actor, key, 1, relationInput(t, map[string]query.Value{"sponsor": query.Integer(key)}))
		if err != nil || result.ID != key || result.Revision != 2 {
			t.Fatal("signed account/choice key was treated as absent", result, err)
		}
		row := relationMember(t, reopen(t), key)
		if row.SponsorID == nil || *row.SponsorID != key || len(relationHistory(t, backend, f.actor, key)) != 1 {
			t.Fatal("signed relation did not persist exactly")
		}
	}
}
