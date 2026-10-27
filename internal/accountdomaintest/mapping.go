package accountdomaintest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func mappingRows() []domain.Mapping {
	return []domain.Mapping{{SourceID: -4, SourceRevision: 1, TargetID: math.MaxInt64, TargetRevision: 2}, {SourceID: 0, SourceRevision: 3, TargetID: 0, TargetRevision: 4}, {SourceID: 17, SourceRevision: 5, TargetID: -7, TargetRevision: 6}}
}

func mappedTransition(t *testing.T, backend Backend, prior domain.Receipt, mappings []domain.Mapping) domain.Receipt {
	t.Helper()
	change := prepare(t, roleB)
	result, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.TransitionMappedIn(ctx, scope, prior, change, instant, mappings)
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readMapping(t *testing.T, backend Backend, receipt domain.Receipt) []domain.Mapping {
	t.Helper()
	rows, err := db.ReadSnapshotResult(t.Context(), backend, func(ctx context.Context, scope db.Queryer) ([]domain.Mapping, error) {
		return domain.LookupMappingsIn(ctx, scope, receipt)
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func mappingHistory(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	if rows := readMapping(t, backend, first); len(rows) != 0 {
		t.Fatal("unmapped ownership invented rows")
	}
	want := mappingRows()
	input := slices.Clone(want)
	slices.Reverse(input)
	second := mappedTransition(t, backend, first, input)
	input[0].TargetID = 999
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte("godj.account-mapping.v1\x00"), payload...))
	if second.MappingCount() != len(want) || second.MappingSHA256() != hex.EncodeToString(digest[:]) {
		t.Fatal("header does not bind actual canonical keys")
	}
	rows := readMapping(t, reopen(t), second)
	if !slices.Equal(rows, want) {
		t.Fatal("reopened correspondence differs")
	}
	rows[0].TargetID = 999
	if !slices.Equal(readMapping(t, backend, second), want) {
		t.Fatal("returned table aliases stored mapping")
	}
	third, err := transition(t.Context(), backend, second, prepare(t, roleA))
	if err != nil {
		t.Fatal(err)
	}
	if third.MappingCount() != 0 || third.MappingSHA256() != "" || !third.Follows(second) {
		t.Fatal("mapping leaked into subsequent ownership")
	}
	if !slices.Equal(readMapping(t, reopen(t), second), want) {
		t.Fatal("historical correspondence lost after later transition")
	}
	observed, err := db.ReadSnapshotResult(t.Context(), reopen(t), domain.InspectIn)
	if err != nil || observed.OperationID() != third.OperationID() {
		t.Fatal("full mapped history inspection", err)
	}
	var retained db.Queryer
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		retained = scope
		_, err := domain.LookupMappingsIn(t.Context(), scope, second)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if rows, err := domain.LookupMappingsIn(t.Context(), retained, second); err == nil || rows != nil {
		t.Fatal("expired mapping lookup published rows")
	}
}

func mappingPages(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	var historical domain.Receipt
	last, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		prior := first
		for index := int64(1); index <= 257; index++ {
			change, err := domain.Prepare(ctx, roleB, evidence)
			if err != nil {
				return domain.Receipt{}, err
			}
			prior, err = domain.TransitionMappedIn(ctx, scope, prior, change, instant, []domain.Mapping{{SourceID: index, SourceRevision: 1, TargetID: -index, TargetRevision: 1}})
			if err != nil {
				return domain.Receipt{}, err
			}
			if index == 1 {
				historical = prior
			}
		}
		return prior, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := reopen(t)
	observed, err := db.ReadSnapshotResult(t.Context(), reader, domain.InspectIn)
	if err != nil || observed.OperationID() != last.OperationID() || observed.Sequence() != 258 {
		t.Fatal("mapping history was truncated at a page boundary", err)
	}
	if got := readMapping(t, reader, historical); len(got) != 1 || got[0].SourceID != 1 || got[0].TargetID != -1 {
		t.Fatal("old correspondence changed across pages")
	}
	header, err := models.TransitionObjects.Using(reader).Filter(models.TransitionFields.OperationID.Exact(historical.OperationID())).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, err := models.MappingObjects.Using(reader).Filter(models.MappingFields.TransitionID.Exact(header.ID)).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.MappingObjects.Delete(t.Context(), reader, &row); err != nil {
		t.Fatal(err)
	}
	if got, err := db.ReadSnapshotResult(t.Context(), reader, domain.InspectIn); !errors.Is(err, domain.ErrCorrupt) || got.Sequence() != 0 {
		t.Fatal("missing historical payload escaped the independent count", err)
	}
}

type mappingRewrite struct {
	db.Session
	rewrites *int
}

func (scope mappingRewrite) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope mappingRewrite) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	id, err := scope.Session.Insert(ctx, plan)
	if err != nil || plan.Table() != "godj_account_domain_mapping" {
		return id, err
	}
	row, err := models.MappingObjects.Using(scope.Session).Filter(models.MappingFields.ID.Exact(id)).Get(ctx)
	if err != nil {
		return 0, err
	}
	var rows []domain.Mapping
	if err := json.Unmarshal([]byte(row.Payload), &rows); err != nil {
		return 0, err
	}
	rows[0].TargetID = 333
	payload, err := json.Marshal(rows)
	if err != nil {
		return 0, err
	}
	prepared, err := models.MappingObjects.PrepareValues(&row, map[string]query.Value{"payload": query.String(string(payload))})
	if err != nil {
		return 0, err
	}
	row, err = prepared.Model()
	if err != nil {
		return 0, err
	}
	if err := prepared.Save(ctx, scope.Session, &row); err != nil {
		return 0, err
	}
	header, err := models.TransitionObjects.Using(scope.Session).Filter(models.TransitionFields.ID.Exact(row.TransitionID)).Get(ctx)
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(append([]byte("godj.account-mapping.v1\x00"), payload...))
	updated, err := models.TransitionObjects.PrepareValues(&header, map[string]query.Value{"mapping_sha256": query.String(hex.EncodeToString(digest[:]))})
	if err != nil {
		return 0, err
	}
	header, err = updated.Model()
	if err != nil {
		return 0, err
	}
	if err := updated.Save(ctx, scope.Session, &header); err != nil {
		return 0, err
	}
	*scope.rewrites++
	return id, nil
}

func mappingWriteReadback(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	change := prepare(t, roleB)
	rewrites := 0
	result, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.TransitionMappedIn(ctx, mappingRewrite{scope, &rewrites}, first, change, instant, mappingRows())
	})
	if err == nil || result.Sequence() != 0 || rewrites != 1 {
		t.Fatal("native rewrite of both payload and checksum published a receipt", err)
	}
	if receipt, err := domain.Lookup(t.Context(), reopen(t), change.OperationID()); err != domain.ErrUninitialized || receipt.Sequence() != 0 {
		t.Fatal("rewritten mapping escaped rollback", err)
	}
	if observed, err := db.ReadSnapshotResult(t.Context(), backend, domain.InspectIn); err != nil || observed.OperationID() != first.OperationID() {
		t.Fatal("rejected mapping changed the prior history", err)
	}
}

type mappingInsertFailure struct {
	db.Session
	writes *int
}

func (scope mappingInsertFailure) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope mappingInsertFailure) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() == "godj_account_domain_mapping" {
		*scope.writes++
		return 0, errors.New("private mapping insertion failure")
	}
	return scope.Session.Insert(ctx, plan)
}

func mappingFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	for _, bad := range [][]domain.Mapping{nil, {{SourceID: 0, SourceRevision: 1, TargetID: 1, TargetRevision: 1}, {SourceID: 0, SourceRevision: 1, TargetID: 2, TargetRevision: 1}}, {{SourceID: 1, SourceRevision: 1, TargetID: 0, TargetRevision: 1}, {SourceID: 2, SourceRevision: 1, TargetID: 0, TargetRevision: 1}}, {{SourceRevision: 0, TargetRevision: 1}}, {{SourceRevision: 1, TargetRevision: 0}}, make([]domain.Mapping, domain.MaximumMappings+1)} {
		change := prepare(t, roleB)
		result, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
			return domain.TransitionMappedIn(ctx, scope, first, change, instant, bad)
		})
		if !errors.Is(err, domain.ErrInvalid) || result.Sequence() != 0 {
			t.Fatal("invalid mapping accepted inside live native scope", err)
		}
	}
	change := prepare(t, roleB)
	writes := 0
	result, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.TransitionMappedIn(ctx, mappingInsertFailure{scope, &writes}, first, change, instant, mappingRows())
	})
	if err == nil || result.Sequence() != 0 || writes != 1 {
		t.Fatal("mapping failure did not follow real header insertion")
	}
	if receipt, err := domain.Lookup(t.Context(), reopen(t), change.OperationID()); err != domain.ErrUninitialized || receipt.Sequence() != 0 {
		t.Fatal("failed ledger left a durable header", err)
	}
	count, err := models.MappingObjects.Using(reopen(t)).Count(t.Context())
	if err != nil || count != 0 {
		t.Fatal("failed mapping was stored", err)
	}
	fault := errors.New("after complete mapped write")
	result, err = db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		receipt, err := domain.TransitionMappedIn(ctx, scope, first, change, instant, mappingRows())
		if err != nil {
			return domain.Receipt{}, err
		}
		return receipt, fault
	})
	if !errors.Is(err, fault) || result.Sequence() != 0 {
		t.Fatal("outer rollback published mapped receipt")
	}
	if _, err := domain.Lookup(t.Context(), backend, change.OperationID()); err != domain.ErrUninitialized {
		t.Fatal("native rollback retained mapped receipt", err)
	}
	second := mappedTransition(t, backend, first, mappingRows())
	if _, err := domain.LookupMappingsIn(t.Context(), backend, second); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("root mapping lookup accepted", err)
	}
	cleanup := snapshotFunc(func(ctx context.Context, read func(db.Queryer) error) error {
		if err := backend.ReadSnapshot(ctx, read); err != nil {
			return err
		}
		return fault
	})
	rows, err := db.ReadSnapshotResult(t.Context(), cleanup, func(ctx context.Context, scope db.Queryer) ([]domain.Mapping, error) {
		return domain.LookupMappingsIn(ctx, scope, second)
	})
	if !errors.Is(err, fault) || rows != nil {
		t.Fatal("cleanup published mapping data", err)
	}
}

func mappingCorruption(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	first := initialize(t, backend)
	receipt := mappedTransition(t, backend, first, mappingRows())
	header, err := models.TransitionObjects.Using(backend).Filter(models.TransitionFields.OperationID.Exact(receipt.OperationID())).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, err := models.MappingObjects.Using(backend).Filter(models.MappingFields.TransitionID.Exact(header.ID)).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	patchHeader := func(values map[string]query.Value) {
		t.Helper()
		current, err := models.TransitionObjects.Using(backend).Filter(models.TransitionFields.ID.Exact(header.ID)).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := models.TransitionObjects.PrepareValues(&current, values)
		if err != nil {
			t.Fatal(err)
		}
		value, err := prepared.Model()
		if err != nil {
			t.Fatal(err)
		}
		if err := prepared.Save(t.Context(), backend, &value); err != nil {
			t.Fatal(err)
		}
	}
	patchPayload := func(payload string) {
		t.Helper()
		current, err := models.MappingObjects.Using(backend).Filter(models.MappingFields.ID.Exact(row.ID)).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := models.MappingObjects.PrepareValues(&current, map[string]query.Value{"payload": query.String(payload)})
		if err != nil {
			t.Fatal(err)
		}
		value, err := prepared.Model()
		if err != nil {
			t.Fatal(err)
		}
		if err := prepared.Save(t.Context(), backend, &value); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"whitespace", "unknown_member", "duplicate_member", "duplicate_source", "duplicate_target", "wrong_count", "wrong_digest", "zero_header", "missing_payload"} {
		t.Run(mode, func(t *testing.T) {
			payload := row.Payload
			count := int64(receipt.MappingCount())
			digest := receipt.MappingSHA256()
			switch mode {
			case "whitespace":
				payload = " " + payload
			case "unknown_member":
				payload = strings.Replace(payload, `"source_id":`, `"unknown":1,"source_id":`, 1)
			case "duplicate_member":
				payload = strings.Replace(payload, `"source_id":-4`, `"source_id":-4,"source_id":-4`, 1)
			case "duplicate_source", "duplicate_target":
				rows := mappingRows()
				if mode == "duplicate_source" {
					rows[1].SourceID = rows[0].SourceID
				} else {
					rows[1].TargetID = rows[0].TargetID
				}
				encoded, _ := json.Marshal(rows)
				payload = string(encoded)
			case "wrong_count":
				count++
			case "wrong_digest":
				digest = strings.Repeat("f", 64)
			case "zero_header":
				count, digest = 0, ""
			case "missing_payload":
				if _, err := models.MappingObjects.Delete(t.Context(), backend, &row); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "wrong_digest" && mode != "zero_header" {
				sum := sha256.Sum256(append([]byte("godj.account-mapping.v1\x00"), []byte(payload)...))
				digest = hex.EncodeToString(sum[:])
			}
			if mode != "missing_payload" {
				patchPayload(payload)
			}
			patchHeader(map[string]query.Value{"mapping_count": query.Integer(count), "mapping_sha256": query.String(digest)})
			actual, err := domain.Lookup(t.Context(), backend, receipt.OperationID())
			if err != nil {
				t.Fatal("fixture header must remain independently valid", err)
			}
			values, err := db.ReadSnapshotResult(t.Context(), backend, func(ctx context.Context, scope db.Queryer) ([]domain.Mapping, error) {
				return domain.LookupMappingsIn(ctx, scope, actual)
			})
			if !errors.Is(err, domain.ErrCorrupt) || values != nil {
				t.Fatal("mapping corruption published data", err)
			}
			if latest, err := db.ReadSnapshotResult(t.Context(), backend, domain.InspectIn); !errors.Is(err, domain.ErrCorrupt) || latest.Sequence() != 0 {
				t.Fatal("startup ignored mapped history corruption", err)
			}
			if mode == "missing_payload" {
				row, err = models.MappingObjects.Create(t.Context(), backend, models.NewMappingCreate(header.ID, row.Payload))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				patchPayload(row.Payload)
			}
			patchHeader(map[string]query.Value{"mapping_count": query.Integer(int64(receipt.MappingCount())), "mapping_sha256": query.String(receipt.MappingSHA256())})
		})
	}
	if _, err := db.ReadSnapshotResult(t.Context(), backend, domain.InspectIn); err != nil {
		t.Fatal("fixture restoration failed", err)
	}
}
