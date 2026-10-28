package accountdomaintest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type catalogMappingObservation struct {
	rows  domain.CatalogMappings
	found bool
}

func catalogMappingRows() domain.CatalogMappings {
	return domain.CatalogMappings{
		Groups:      []domain.Mapping{{SourceID: -2, SourceRevision: 1, TargetID: 0, TargetRevision: 3}, {SourceID: 0, SourceRevision: 4, TargetID: -7, TargetRevision: 2}},
		Permissions: []domain.Mapping{{SourceID: 0, SourceRevision: 7, TargetID: 0, TargetRevision: 8}},
	}
}

func lookupCatalogMapping(ctx context.Context, scope db.Queryer, receipt domain.Receipt) (catalogMappingObservation, error) {
	rows, found, err := domain.LookupCatalogMappingsIn(ctx, scope, receipt)
	return catalogMappingObservation{rows, found}, err
}

func catalogMappedTransition(t *testing.T, backend Backend, prior domain.Receipt, mapping domain.CatalogMappings) domain.Receipt {
	t.Helper()
	change := prepare(t, roleB)
	result, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.TransitionMappedIn(ctx, scope, prior, change, instant, mappingRows(), mapping)
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func catalogMappingHistory(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	legacy := mappedTransition(t, backend, first, mappingRows())
	want := catalogMappingRows()
	input := catalogMappingRows()
	slices.Reverse(input.Groups)
	receipt := catalogMappedTransition(t, backend, legacy, input)
	input.Groups[0].TargetID, input.Permissions[0].TargetID = 55, 66
	read := func(receipt domain.Receipt) catalogMappingObservation {
		t.Helper()
		value, err := db.ReadSnapshotResult(t.Context(), reopen(t), func(ctx context.Context, scope db.Queryer) (catalogMappingObservation, error) {
			return lookupCatalogMapping(ctx, scope, receipt)
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, old := range []domain.Receipt{first, legacy} {
		if got := read(old); got.found || got.rows.Groups != nil || got.rows.Permissions != nil {
			t.Fatal("historical receipt invented catalog correspondence")
		}
	}
	got := read(receipt)
	if !got.found || !slices.Equal(got.rows.Groups, want.Groups) || !slices.Equal(got.rows.Permissions, want.Permissions) || !slices.Equal(readMapping(t, reopen(t), receipt), mappingRows()) {
		t.Fatal("reopened catalog/account correspondence differs")
	}
	payload, err := json.Marshal(struct {
		Version  int                    `json:"version"`
		Accounts []domain.Mapping       `json:"accounts"`
		Catalog  domain.CatalogMappings `json:"catalog"`
	}{2, mappingRows(), want})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append([]byte("godj.account-mapping.v2\x00"), payload...))
	if receipt.MappingCount() != len(mappingRows()) || receipt.MappingSHA256() != hex.EncodeToString(sum[:]) {
		t.Fatal("header does not bind accounts and both catalog tables")
	}
	got.rows.Groups[0].TargetID = 888
	empty := catalogMappedTransition(t, backend, receipt, domain.CatalogMappings{})
	if got := read(empty); !got.found || len(got.rows.Groups) != 0 || len(got.rows.Permissions) != 0 {
		t.Fatal("explicit empty catalog is missing")
	}
	if got := read(receipt); !slices.Equal(got.rows.Groups, want.Groups) || !slices.Equal(got.rows.Permissions, want.Permissions) {
		t.Fatal("later transition changed historical catalog")
	}
	if latest, err := db.ReadSnapshotResult(t.Context(), reopen(t), domain.InspectIn); err != nil || latest.OperationID() != empty.OperationID() {
		t.Fatal("mixed historical mapping format inspection", err)
	}
	var retained db.Queryer
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		retained = scope
		_, _, err := domain.LookupCatalogMappingsIn(t.Context(), scope, receipt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if rows, found, err := domain.LookupCatalogMappingsIn(t.Context(), retained, receipt); err == nil || found || rows.Groups != nil || rows.Permissions != nil {
		t.Fatal("expired catalog lookup published values", err)
	}
}

func catalogMappingFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	for _, catalogs := range [][]domain.CatalogMappings{
		{{}, {}},
		{{Groups: []domain.Mapping{{SourceRevision: 0, TargetRevision: 1}}}},
		{{Permissions: []domain.Mapping{{SourceID: 1, SourceRevision: 1, TargetRevision: 1}, {SourceID: 2, SourceRevision: 1, TargetRevision: 1}}}},
		{{Groups: make([]domain.Mapping, domain.MaximumMappings+1)}},
	} {
		change := prepare(t, roleB)
		got, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
			return domain.TransitionMappedIn(ctx, scope, first, change, instant, mappingRows(), catalogs...)
		})
		if !errors.Is(err, domain.ErrInvalid) || got.Sequence() != 0 {
			t.Fatal("invalid catalog correspondence accepted", err)
		}
	}
	for _, mode := range []string{"insert", "outer_rollback", "rewritten_payload"} {
		t.Run(mode, func(t *testing.T) {
			change := prepare(t, roleB)
			faults := 0
			got, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
				var wrapped db.Session = scope
				if mode == "insert" {
					wrapped = mappingInsertFailure{scope, &faults}
				} else if mode == "rewritten_payload" {
					wrapped = mappingRewrite{scope, &faults}
				}
				got, err := domain.TransitionMappedIn(ctx, wrapped, first, change, instant, mappingRows(), catalogMappingRows())
				if err == nil && mode == "outer_rollback" {
					faults++
					return got, errors.New("after complete catalog receipt")
				}
				return got, err
			})
			if err == nil || got.Sequence() != 0 || faults != 1 {
				t.Fatal("catalog fault did not roll back its complete native write", err)
			}
			if got, err := domain.Lookup(t.Context(), reopen(t), change.OperationID()); err != domain.ErrUninitialized || got.Sequence() != 0 {
				t.Fatal("catalog failure left a durable receipt", err)
			}
			if got, err := db.ReadSnapshotResult(t.Context(), reopen(t), domain.InspectIn); err != nil || got.OperationID() != first.OperationID() {
				t.Fatal("catalog failure changed prior ownership", err)
			}
		})
	}
	receipt := catalogMappedTransition(t, backend, first, catalogMappingRows())
	if _, found, err := domain.LookupCatalogMappingsIn(t.Context(), backend, receipt); !errors.Is(err, domain.ErrInvalid) || found {
		t.Fatal("root catalog lookup accepted", err)
	}
	fault := errors.New("private catalog snapshot cleanup")
	cleanup := snapshotFunc(func(ctx context.Context, read func(db.Queryer) error) error {
		if err := backend.ReadSnapshot(ctx, read); err != nil {
			return err
		}
		return fault
	})
	got, err := db.ReadSnapshotResult(t.Context(), cleanup, func(ctx context.Context, scope db.Queryer) (catalogMappingObservation, error) {
		return lookupCatalogMapping(ctx, scope, receipt)
	})
	if !errors.Is(err, fault) || got.found || got.rows.Groups != nil || got.rows.Permissions != nil {
		t.Fatal("cleanup published catalog evidence", err)
	}
}

func catalogMappingCorruption(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	receipt := catalogMappedTransition(t, backend, initialize(t, backend), catalogMappingRows())
	header, err := models.TransitionObjects.Using(backend).Filter(models.TransitionFields.OperationID.Exact(receipt.OperationID())).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, err := models.MappingObjects.Using(backend).Filter(models.MappingFields.TransitionID.Exact(header.ID)).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	patch := func(payload, digest string) {
		t.Helper()
		patchCatalogMappingModel(t, backend, models.MappingObjects, row, map[string]query.Value{"payload": query.String(payload)})
		patchCatalogMappingModel(t, backend, models.TransitionObjects, header, map[string]query.Value{"mapping_sha256": query.String(digest)})
	}
	for _, mode := range []string{"missing_catalog", "unknown_member", "duplicate_member", "duplicate_group", "duplicate_permission_target", "null_groups", "wrong_version", "wrong_prefix"} {
		t.Run(mode, func(t *testing.T) {
			payload := row.Payload
			switch mode {
			case "missing_catalog":
				index := strings.Index(payload, `,"catalog":`)
				payload = payload[:index] + "}"
			case "unknown_member":
				payload = strings.Replace(payload, `"groups":`, `"unknown":0,"groups":`, 1)
			case "duplicate_member":
				payload = strings.Replace(payload, `"version":2`, `"version":2,"version":2`, 1)
			case "duplicate_group":
				payload = strings.Replace(payload, `"groups":[`, `"groups":[{"source_id":-2,"source_revision":1,"target_id":-99,"target_revision":1},`, 1)
			case "duplicate_permission_target":
				payload = strings.Replace(payload, `"permissions":[`, `"permissions":[{"source_id":-3,"source_revision":1,"target_id":0,"target_revision":1},`, 1)
			case "null_groups":
				payload = strings.Replace(payload, `"groups":[`, `"groups":null,"ignored":[`, 1)
			case "wrong_version":
				payload = strings.Replace(payload, `"version":2`, `"version":1`, 1)
			}
			prefix := "godj.account-mapping.v2\x00"
			if mode == "wrong_prefix" {
				prefix = "godj.account-mapping.v1\x00"
			}
			sum := sha256.Sum256(append([]byte(prefix), payload...))
			patch(payload, hex.EncodeToString(sum[:]))
			actual, err := domain.Lookup(t.Context(), backend, receipt.OperationID())
			if err != nil {
				t.Fatal("corruption fixture header invalid", err)
			}
			got, err := db.ReadSnapshotResult(t.Context(), backend, func(ctx context.Context, scope db.Queryer) (catalogMappingObservation, error) {
				return lookupCatalogMapping(ctx, scope, actual)
			})
			if !errors.Is(err, domain.ErrCorrupt) || got.found || got.rows.Groups != nil || got.rows.Permissions != nil {
				t.Fatal("corrupt catalog published evidence", err)
			}
			if got, err := db.ReadSnapshotResult(t.Context(), backend, func(ctx context.Context, scope db.Queryer) ([]domain.Mapping, error) {
				return domain.LookupMappingsIn(ctx, scope, actual)
			}); !errors.Is(err, domain.ErrCorrupt) || got != nil {
				t.Fatal("account-only reader ignored corrupt catalog", err)
			}
			if got, err := db.ReadSnapshotResult(t.Context(), backend, domain.InspectIn); !errors.Is(err, domain.ErrCorrupt) || got.Sequence() != 0 {
				t.Fatal("startup ignored corrupt catalog payload", err)
			}
			patch(row.Payload, receipt.MappingSHA256())
		})
	}
}

func patchCatalogMappingModel[M any](t *testing.T, backend db.Mutator, objects orm.Manager[M], row M, values map[string]query.Value) {
	t.Helper()
	prepared, err := objects.PrepareValues(&row, values)
	if err != nil {
		t.Fatal(err)
	}
	next, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Save(t.Context(), backend, &next); err != nil {
		t.Fatal(err)
	}
}
