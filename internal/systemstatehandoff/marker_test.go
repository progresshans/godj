package systemstatehandoff

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type markerScope struct {
	rows    *markerRows
	failure error
	queries int
}

func (scope *markerScope) ValidateSession(ctx context.Context) error {
	return errors.Join(ctx.Err(), scope.failure)
}
func (scope *markerScope) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	scope.queries++
	if plan.Table() != CredentialTable {
		return nil, ErrInvalid
	}
	return scope.rows, nil
}

type markerRows struct {
	count, index, closed          int
	key                           int64
	password, permissions, digest string
	active                        bool
	scan, iteration, close        error
	afterClose                    func()
}

func (rows *markerRows) Next() bool { rows.index++; return rows.index <= rows.count }
func (rows *markerRows) Scan(values ...any) error {
	if rows.scan != nil {
		return rows.scan
	}
	if len(values) != 5 {
		return ErrInvalid
	}
	*values[0].(*int64), *values[1].(*string), *values[2].(*bool), *values[3].(*string), *values[4].(*string) = rows.key, rows.password, rows.active, rows.permissions, rows.digest
	return nil
}
func (rows *markerRows) Err() error { return rows.iteration }
func (rows *markerRows) Close() error {
	rows.closed++
	if rows.afterClose != nil {
		rows.afterClose()
	}
	return rows.close
}

func TestRetirementRequiresOneExactLiveScopedMarker(t *testing.T) {
	for _, mode := range []string{"complete", "missing", "duplicate", "key", "active", "permissions", "operation", "password", "expired", "scan", "iteration", "close", "cancel_close", "invalid_operation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			operation := strings.Repeat("a", 64)
			password, err := Password(operation)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("private marker read failure")
			rows := &markerRows{count: 1, key: 1, password: password, permissions: "v1.AAA", digest: "sha256:" + operation}
			scope := &markerScope{rows: rows}
			switch mode {
			case "missing":
				rows.count = 0
			case "duplicate":
				rows.count = 2
			case "key":
				rows.key = 0
			case "active":
				rows.active = true
			case "permissions":
				rows.permissions += "="
			case "operation":
				rows.digest = "sha256:" + strings.Repeat("b", 64)
			case "password":
				rows.password = "!unrelated"
			case "expired":
				scope.failure = failure
			case "scan":
				rows.scan = failure
			case "iteration":
				rows.iteration = failure
			case "close":
				rows.close = failure
			case "cancel_close":
				rows.afterClose = cancel
			case "invalid_operation":
				operation = strings.Repeat("A", 64)
			}
			err = RequireRetired(ctx, scope, operation)
			if (err == nil) != (mode == "complete") {
				t.Fatal("incomplete marker published retirement proof", mode, err)
			}
			if scope.queries != 0 && rows.closed != 1 {
				t.Fatal("marker reader lost rowset ownership", rows.closed)
			}
			if mode == "expired" || mode == "scan" || mode == "iteration" || mode == "close" {
				if !errors.Is(err, failure) {
					t.Fatal("marker read lost its actual failure", err)
				}
			}
			if mode == "invalid_operation" && scope.queries != 0 {
				t.Fatal("invalid operation performed I/O")
			}
		})
	}
}

func TestRetirementOperationCannotBeGuessedFromOtherPasswordStates(t *testing.T) {
	for _, operation := range []string{"", strings.Repeat("0", 64), strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("z", 64)} {
		if password, err := Password(operation); err == nil || password != "" {
			t.Fatal("invalid operation encoded a retirement marker")
		}
	}
	for _, password := range []string{"!disabled", "!godj-identity-transferred", "plain", passwordPrefix, passwordPrefix + strings.Repeat("A", 64)} {
		if _, ok := RetiredOperation(password); ok {
			t.Fatal("unrelated password state admitted as retirement")
		}
	}
	want := strings.Repeat("d", 64)
	password, _ := Password(want)
	if got, ok := RetiredOperation(password); !ok || got != want {
		t.Fatal("valid retirement marker did not round trip")
	}
}
