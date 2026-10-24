package accountstoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func firstAccountCommitFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	group, permission := firstAccountChoices(t, backend)
	for _, mode := range []string{"skipped", "nil_session", "root_session", "domain_failure", "write_failure", "group_failure", "direct_failure", "audit_failure", "repeated", "validator_rejection", "validator_failure", "cleanup_failure", "swallowed_rejection", "invalid_clock", "cancel_confirmed_rollback", "unknown_rollback", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) { firstAccountFailureCase(t, backend, reopen, mode, group.ID, permission.ID) })
	}
}
func firstAccountUnknownCommit(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	group, permission := firstAccountChoices(t, backend)
	firstAccountFailureCase(t, backend, reopen, "unknown_commit", group.ID, permission.ID)
}
func firstAccountFailureCase(t *testing.T, backend Backend, reopen func(*testing.T) Backend, mode string, group, permission int64) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fault := errors.New("private first-account fault")
	backendOwner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
	effects := map[string]int{}
	if mode == "domain_failure" || mode == "write_failure" || mode == "group_failure" || mode == "direct_failure" || mode == "audit_failure" {
		backendOwner.mode = "first_" + mode
		backendOwner.decorate = func(scope db.Session) db.Session {
			relation, ok := scope.(db.RelationSession)
			if !ok {
				t.Fatal("lost native relation scope")
			}
			conflict, ok := scope.(db.ConflictInserter)
			if !ok {
				t.Fatal("lost native conflict inserter")
			}
			return &roleFaultScope{RelationSession: relation, conflict: conflict, mode: mode, fault: fault, effects: effects}
		}
	}
	config := firstAccountConfig()
	commit := false
	config.Validate = func(context.Context, db.Queryer, models.User, accountmodel.RoleMemberships) (validation.Errors, error) {
		if commit && (mode == "validator_rejection" || mode == "validator_failure" || mode == "cleanup_failure" || mode == "swallowed_rejection") {
			result := validation.NewErrors(validation.New("first_name", "policy"))
			if mode == "validator_failure" {
				return result, fault
			}
			return result, nil
		}
		return validation.Errors{}, nil
	}
	config.Now = func() time.Time {
		if mode == "invalid_clock" && commit {
			return time.Time{}
		}
		return joined
	}
	hasher := &creationHasher{PasswordHasher: firstAccountHasher(t)}
	owner := firstAccountOwner(t, backendOwner, hasher, config)
	operation := sha256.Sum256([]byte("first fault " + mode))
	operationID := hex.EncodeToString(operation[:])
	input := roleInput(t, map[string]query.Value{"username": query.String("first-" + mode)}, map[string][]int64{"groups": {group}, "permissions": {permission}})
	pending, err := owner.PrepareOperation(ctx, operationID, input, password)
	if err != nil {
		t.Fatal("first failure fixture preparation", err)
	}
	commit = true
	result, err := owner.Commit(ctx, pending)
	committed := mode == "unknown_commit" || mode == "post_commit_cancel"
	if backendOwner.calls != 1 || hasher.calls.Load() != 1 {
		t.Fatal("first operation retried or rehashed")
	}
	if mode == "post_commit_cancel" {
		if err != nil || result.Account.Revision != 1 || result.Ownership.Sequence() != 1 {
			t.Fatal("late cancellation erased known first commit", err)
		}
	} else {
		if err == nil || !reflect.DeepEqual(result, accountmodel.FirstAccountCreation{}) {
			t.Fatal("first failure published provisional identity", err)
		}
		if mode == "validator_rejection" {
			if fields, rejected := validation.Rejected(err); !rejected || fields.ByField("first_name").Empty() {
				t.Fatal("first policy refusal lost", err)
			}
		} else {
			if _, rejected := validation.Rejected(err); rejected {
				t.Fatal("first execution failure became validation")
			}
			code := accountmodel.CodePersistence
			if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "unknown_rollback" || mode == "unknown_commit" {
				code = accountmodel.CodeOutcomeUnknown
			}
			requireProfileCode(t, err, code)
		}
		for _, text := range []string{fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			if strings.Contains(text, fault.Error()) || strings.Contains(text, password) {
				t.Fatal("first failure leaked private cause")
			}
		}
	}
	if pending.OperationID() != operationID {
		t.Fatal("reconciliation ID was discarded")
	}
	if _, err := owner.Commit(t.Context(), pending); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
		t.Fatal("failed first attempt reused", err)
	}
	counts := firstAccountCounts(t, reopen(t))
	want := int64(0)
	if committed {
		want = 1
	}
	if !reflect.DeepEqual(counts, []int64{want, want, want, want, want}) {
		t.Fatal("partial domain/account/membership/audit survived", mode, counts)
	}
	receipt, lookupError := domain.Lookup(t.Context(), reopen(t), operationID)
	if committed {
		if lookupError != nil || receipt.OperationID() != operationID || receipt.Sequence() != 1 {
			t.Fatal("unknown first commit lost reconciliation receipt", lookupError)
		}
	} else if !errors.Is(lookupError, domain.ErrUninitialized) || receipt.Sequence() != 0 {
		t.Fatal("rolled-back first write left receipt", lookupError)
	}
	tables := []string{"godj_account_domain_transition", "godj_identity_user", "godj_identity_user_groups", "godj_identity_user_permissions", "godj_account_audit_event"}
	failing := map[string]int{"domain_failure": 0, "write_failure": 1, "group_failure": 2, "direct_failure": 3, "audit_failure": 4}
	if last, ok := failing[mode]; ok {
		for _, table := range tables[:last+1] {
			if effects[table] != 1 {
				t.Fatal("first fault not after actual native effect", mode, table, effects)
			}
		}
	}
}

func firstAccountReadFailures(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	for _, mode := range []string{"wrapped_absence", "close_failure", "swallowed_absence", "validation_cleanup", "root_scope"} {
		t.Run(mode, func(t *testing.T) {
			fault := errors.New("private first-domain read failure")
			failure := errors.Join(domain.ErrUninitialized, fault)
			backendOwner := &creationSnapshotBackend{Backend: backend, read: snapshotFunc(func(ctx context.Context, observe func(db.Queryer) error) error {
				if mode == "root_scope" {
					return observe(backend)
				}
				err := backend.ReadSnapshot(ctx, func(scope db.Queryer) error {
					probe := &profileReadProbe{Queryer: scope, validator: scope.(db.SessionValidator)}
					if mode == "wrapped_absence" || mode == "swallowed_absence" {
						probe.fault = failure
					}
					if mode == "close_failure" {
						probe.fault, probe.closeFail = failure, true
					}
					err := observe(probe)
					if mode == "swallowed_absence" {
						return nil
					}
					return err
				})
				if mode == "validation_cleanup" {
					return errors.Join(err, fault)
				}
				return err
			})}
			config := firstAccountConfig()
			if mode == "validation_cleanup" {
				config.Validate = func(context.Context, db.Queryer, models.User, accountmodel.RoleMemberships) (validation.Errors, error) {
					return validation.NewErrors(validation.New("first_name", "policy")), nil
				}
			}
			hasher := &creationHasher{PasswordHasher: firstAccountHasher(t)}
			owner := firstAccountOwner(t, backendOwner, hasher, config)
			pending, err := owner.Prepare(t.Context(), creationInput(t, "first-read"), password)
			if pending.OperationID() != "" || hasher.calls.Load() != 0 {
				t.Fatal("failed empty-domain observation published a candidate")
			}
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if _, rejected := validation.Rejected(err); rejected {
				t.Fatal("domain/read cleanup became normal refusal")
			}
			if mode != "root_scope" && !errors.Is(err, fault) {
				t.Fatal("empty-domain read failure cause lost", err)
			}
			if counts := firstAccountCounts(t, backend); !reflect.DeepEqual(counts, []int64{0, 0, 0, 0, 0}) {
				t.Fatal("failed first read mutated state")
			}
		})
	}
}
