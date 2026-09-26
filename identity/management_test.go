package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestManagementRejectsInvalidConfigurationAndInputWithoutIO(t *testing.T) {
	if _, err := NewManager(nil, nil, nil); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatal(err)
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "manager", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{}
	for _, input := range []struct {
		ctx          context.Context
		id, revision int64
		password     string
	}{
		{nil, 1, 1, "secret"}, {t.Context(), 0, 1, "secret"}, {t.Context(), 1, 0, "secret"}, {t.Context(), 1, math.MaxInt64, "secret"}, {t.Context(), 1, 1, ""},
	} {
		if result, err := manager.SetPassword(input.ctx, actor, input.id, input.revision, input.password); !errors.Is(err, &Error{Code: CodeInvalidInput}) || result.ID != 0 {
			t.Fatal("invalid input accepted", err)
		}
	}
	if result, err := manager.SetPassword(t.Context(), actor, 1, 1, "secret"); !errors.Is(err, &Error{Code: CodeInvalidConfig}) || result.ID != 0 {
		t.Fatal("zero manager accepted", err)
	}
	var nilManager *Manager
	if _, err := nilManager.SetPassword(t.Context(), actor, 1, 1, "secret"); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatal("nil manager accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := manager.SetPassword(ctx, actor, 1, 1, "secret"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestManagementErrorsKeepDiagnosticFallbackOpaque(t *testing.T) {
	const secret = "private-management-cause"
	cause := errors.New(secret)
	err := managementError(CodePersistence, "transaction", cause)
	if !errors.Is(err, cause) {
		t.Fatal("cause lost")
	}
	for _, value := range []any{err, *err.(*Error)} {
		data, marshalErr := json.Marshal(value)
		if marshalErr != nil || strings.Contains(string(data), secret) {
			t.Fatal("JSON leaked private cause")
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%d", "%f", "%p", "%w"} {
			if strings.Contains(fmt.Sprintf(format, value), secret) {
				t.Fatal("format leaked cause", format)
			}
		}
	}
}
