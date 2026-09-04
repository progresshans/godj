package systemstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

func TestPermissionCodecIsDeterministicBoundedAndCurrentOnly(t *testing.T) {
	permissions := []auth.Permission{
		mustPermission(t, "article.article.view"),
		mustPermission(t, "article.article.change"),
		mustPermission(t, "article.article.delete"),
	}
	first, err := encodePermissions(permissions)
	if err != nil {
		t.Fatalf("encodePermissions(first): %v", err)
	}
	second, err := encodePermissions(append([]auth.Permission(nil), permissions...))
	if err != nil {
		t.Fatalf("encodePermissions(second): %v", err)
	}
	if first != second || !strings.HasPrefix(first, permissionPayloadPrefix) || len(first) > credentialPermissionsMaxLength {
		t.Fatalf("permission payload is unstable or out of bounds")
	}
	decoded, err := decodePermissions(first)
	if err != nil {
		t.Fatalf("decodePermissions(current): %v", err)
	}
	if !reflect.DeepEqual(decoded, permissions) {
		t.Fatalf("decoded permissions = %v, want %v", decoded, permissions)
	}
	reversed := []auth.Permission{permissions[2], permissions[1], permissions[0]}
	reversedPayload, err := encodePermissions(reversed)
	if err != nil {
		t.Fatalf("encodePermissions(reversed): %v", err)
	}
	if reversedPayload == first {
		t.Fatal("permission codec discarded declaration order")
	}

	empty, err := encodePermissions(nil)
	if err != nil {
		t.Fatalf("encodePermissions(empty): %v", err)
	}
	if decoded, err := decodePermissions(empty); err != nil || len(decoded) != 0 {
		t.Fatalf("decodePermissions(empty) = (%v,%v)", decoded, err)
	}

	maximum := make([]auth.Permission, 256)
	for index := range maximum {
		maximum[index] = mustPermission(t, fmt.Sprintf("system.p%03d", index))
	}
	maximumPayload, err := encodePermissions(maximum)
	if err != nil || len(maximumPayload) > credentialPermissionsMaxLength {
		t.Fatalf("encodePermissions(maximum) = len %d/error %v", len(maximumPayload), err)
	}
	tooMany := append(append([]auth.Permission(nil), maximum...), mustPermission(t, "system.overflow"))
	if _, err := encodePermissions(tooMany); !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "permissions"}) {
		t.Fatalf("encodePermissions(too many) error = %#v", err)
	}
}

func TestPermissionCodecRejectsUnknownMalformedDuplicateAndSecretBearingInput(t *testing.T) {
	marker := "SHOULD_NOT_ESCAPE"
	candidates := []string{
		"",
		"v2.AAA",
		"v1.***",
		"v1.AAA=",
		rawPermissionPayload(t, []string{"article.article.view", "article.article.view"}),
		rawPermissionPayload(t, []string{marker}),
		permissionPayloadPrefix + strings.Repeat("A", credentialPermissionsMaxLength),
	}
	for _, candidate := range candidates {
		decoded, err := decodePermissions(candidate)
		if !errors.Is(err, &Error{Code: CodeCorruptState, Field: "permissions"}) || decoded != nil {
			t.Fatalf("decodePermissions(malformed) = (%v,%#v)", decoded, err)
		}
		if strings.Contains(err.Error(), marker) || (candidate != "" && strings.Contains(err.Error(), candidate)) {
			t.Fatalf("permission decoder leaked stored payload through %q", err)
		}
	}
}

func TestProvisionOperatorConfigJSONCannotPopulateSecretFields(t *testing.T) {
	const (
		passwordMarker = "operator-password-secret-marker"
		hasherMarker   = "operator-password-hasher-secret-marker"
	)
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true})
	if err != nil {
		t.Fatalf("auth.NewPrincipal(): %v", err)
	}
	config := ProvisionOperatorConfig{
		Username: "admin",
		Password: passwordMarker,
		CredentialPolicy: CredentialPolicy{
			Principal:      principal,
			PasswordHasher: bootstrapMarkerHasher{Pepper: hasherMarker},
		},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal(ProvisionOperatorConfig): %v", err)
	}
	for _, rendered := range []string{fmt.Sprint(config), fmt.Sprintf("%#v", config)} {
		if rendered != "systemstate.ProvisionOperatorConfig{redacted}" ||
			strings.Contains(rendered, passwordMarker) || strings.Contains(rendered, hasherMarker) {
			t.Fatalf("ProvisionOperatorConfig formatting = %q", rendered)
		}
	}
	if strings.Contains(string(encoded), passwordMarker) || strings.Contains(string(encoded), hasherMarker) ||
		strings.Contains(string(encoded), `"Password"`) || strings.Contains(string(encoded), `"PasswordHasher"`) {
		t.Fatalf("ProvisionOperatorConfig JSON publishes a secret-bearing field: %s", encoded)
	}
	var decoded ProvisionOperatorConfig
	if err := json.Unmarshal([]byte(`{"Username":"admin","Password":"`+passwordMarker+`","CredentialPolicy":{"PasswordHasher":{"Pepper":"`+hasherMarker+`"}}}`), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ProvisionOperatorConfig): %v", err)
	}
	if decoded.Password != "" || decoded.CredentialPolicy.PasswordHasher != nil {
		t.Fatal("ProvisionOperatorConfig JSON populated a secret-bearing field")
	}
}

func TestReadCredentialRowsClosesRowsReturnedWithPrimaryError(t *testing.T) {
	primary := errors.New("credential query primary failure")
	rows := &credentialFaultRows{closeErr: errors.New("credential rows close failure")}
	result, err := readCredentialRows(context.Background(), credentialFaultQueryer{rows: rows, err: primary})
	if result != nil || !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: credentialTableName}) ||
		!errors.Is(err, primary) {
		t.Fatalf("readCredentialRows(rows+error) = (%v, %#v)", result, err)
	}
	if rows.closeCalls != 1 || rows.nextCalls != 0 || rows.scanCalls != 0 || rows.errCalls != 0 {
		t.Fatalf(
			"rows+error calls = close %d/next %d/scan %d/err %d, want 1/0/0/0",
			rows.closeCalls, rows.nextCalls, rows.scanCalls, rows.errCalls,
		)
	}
}

func TestReadCredentialRowsRejectsTypedNilRowsWithoutMethodCalls(t *testing.T) {
	primary := errors.New("credential typed-nil primary failure")
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "nil error"},
		{name: "primary error", err: primary},
	} {
		t.Run(test.name, func(t *testing.T) {
			var rows *credentialFaultRows
			result, err := readCredentialRows(context.Background(), credentialFaultQueryer{rows: rows, err: test.err})
			if result != nil || !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: credentialTableName}) {
				t.Fatalf("readCredentialRows(typed nil) = (%v, %#v)", result, err)
			}
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("readCredentialRows(typed nil) lost primary error: %#v", err)
			}
		})
	}
}

func TestSystemStateQueryAcquisitionRejectsTypedNilRowsAndClosesRowsWithError(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, credentialFaultQueryer) error
	}{
		{
			name: "provision session inspection",
			run: func(ctx context.Context, queryer credentialFaultQueryer) error {
				_, err := inspectProvisionSessionTable(ctx, queryer)
				return err
			},
		},
		{
			name: "provision audit inspection",
			run: func(ctx context.Context, queryer credentialFaultQueryer) error {
				_, err := inspectProvisionAuditTable(ctx, queryer)
				return err
			},
		},
		{
			name: "session inventory",
			run: func(ctx context.Context, queryer credentialFaultQueryer) error {
				_, err := scanSessionInventory(ctx, queryer, 1)
				return err
			},
		},
		{
			name: "session payloads",
			run: func(ctx context.Context, queryer credentialFaultQueryer) error {
				_, err := scanSessionPayloads(ctx, queryer, sessions.Limits{}, 1, nil)
				return err
			},
		},
		{
			name: "session rows",
			run: func(ctx context.Context, queryer credentialFaultQueryer) error {
				_, err := readSessionRows(ctx, queryer, query.NewPlan(sessionTableName, []query.FieldRef{
					systemRowIDField,
					sessionDigestField,
					sessionPayloadField,
				}), 1)
				return err
			},
		},
		{
			name: "audit inspection",
			run: func(ctx context.Context, queryer credentialFaultQueryer) error {
				_, err := inspectAuditTable(ctx, queryer, 1)
				return err
			},
		},
		{
			name: "audit rows",
			run: func(ctx context.Context, queryer credentialFaultQueryer) error {
				_, err := queryAuditRows(ctx, queryer, "", 0, 1, query.Ascending)
				return err
			},
		},
		{
			name: "audit prune",
			run: func(ctx context.Context, queryer credentialFaultQueryer) error {
				return pruneAuditRows(ctx, credentialFaultSession{credentialFaultQueryer: queryer}, 1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Run("typed nil success", func(t *testing.T) {
				var rows *credentialFaultRows
				if err := test.run(context.Background(), credentialFaultQueryer{rows: rows}); err == nil {
					t.Fatal("typed-nil rows with nil error were accepted")
				}
			})

			t.Run("typed nil primary error", func(t *testing.T) {
				primary := errors.New("typed-nil query primary failure")
				var rows *credentialFaultRows
				err := test.run(context.Background(), credentialFaultQueryer{rows: rows, err: primary})
				if err == nil || !errors.Is(err, primary) {
					t.Fatalf("typed-nil rows lost primary error: %#v", err)
				}
			})

			t.Run("rows plus primary error", func(t *testing.T) {
				primary := errors.New("rows query primary failure")
				rows := &credentialFaultRows{closeErr: errors.New("rows close failure")}
				err := test.run(context.Background(), credentialFaultQueryer{rows: rows, err: primary})
				if err == nil || !errors.Is(err, primary) {
					t.Fatalf("rows+error lost primary error: %#v", err)
				}
				if rows.closeCalls != 1 || rows.nextCalls != 0 || rows.scanCalls != 0 || rows.errCalls != 0 {
					t.Fatalf(
						"rows+error calls = close %d/next %d/scan %d/err %d, want 1/0/0/0",
						rows.closeCalls, rows.nextCalls, rows.scanCalls, rows.errCalls,
					)
				}
			})
		})
	}
}

type bootstrapMarkerHasher struct {
	auth.PasswordHasher
	Pepper string
}

type credentialFaultQueryer struct {
	rows db.Rows
	err  error
}

type credentialFaultSession struct {
	credentialFaultQueryer
}

func (credentialFaultSession) Insert(context.Context, query.InsertPlan) (int64, error) {
	panic("typed-nil query acquisition must not insert")
}

func (credentialFaultSession) Update(context.Context, query.UpdatePlan) (int64, error) {
	panic("typed-nil query acquisition must not update")
}

func (credentialFaultSession) Delete(context.Context, query.DeletePlan) (int64, error) {
	panic("typed-nil query acquisition must not delete")
}

func (queryer credentialFaultQueryer) Query(context.Context, query.Plan) (db.Rows, error) {
	return queryer.rows, queryer.err
}

type credentialFaultRows struct {
	closeErr   error
	closeCalls int
	nextCalls  int
	scanCalls  int
	errCalls   int
}

func (rows *credentialFaultRows) Next() bool {
	if rows == nil {
		panic("typed-nil credential rows Next must not be called")
	}
	rows.nextCalls++
	return false
}

func (rows *credentialFaultRows) Scan(...any) error {
	if rows == nil {
		panic("typed-nil credential rows Scan must not be called")
	}
	rows.scanCalls++
	return nil
}

func (rows *credentialFaultRows) Err() error {
	if rows == nil {
		panic("typed-nil credential rows Err must not be called")
	}
	rows.errCalls++
	return nil
}

func (rows *credentialFaultRows) Close() error {
	if rows == nil {
		panic("typed-nil credential rows Close must not be called")
	}
	rows.closeCalls++
	return rows.closeErr
}

func mustPermission(t *testing.T, value string) auth.Permission {
	t.Helper()
	permission, err := auth.NewPermission(value)
	if err != nil {
		t.Fatalf("auth.NewPermission(%q): %v", value, err)
	}
	return permission
}

func rawPermissionPayload(t *testing.T, values []string) string {
	t.Helper()
	buffer := bytes.NewBuffer(nil)
	if err := binary.Write(buffer, binary.BigEndian, uint16(len(values))); err != nil {
		t.Fatalf("write permission count: %v", err)
	}
	for _, value := range values {
		if err := binary.Write(buffer, binary.BigEndian, uint16(len(value))); err != nil {
			t.Fatalf("write permission length: %v", err)
		}
		if _, err := buffer.WriteString(value); err != nil {
			t.Fatalf("write permission value: %v", err)
		}
	}
	return permissionPayloadPrefix + base64.RawURLEncoding.EncodeToString(buffer.Bytes())
}
