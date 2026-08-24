package systemstate

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
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

func TestBootstrapConfigFormattingIsRedacted(t *testing.T) {
	const marker = "bootstrap-password-secret-marker"
	config := BootstrapConfig{
		Username:    "admin",
		Password:    marker,
		PrincipalID: "operator",
		Active:      true,
	}
	for _, rendered := range []string{fmt.Sprint(config), fmt.Sprintf("%#v", config)} {
		if rendered != "systemstate.BootstrapConfig{redacted}" || strings.Contains(rendered, marker) {
			t.Fatalf("BootstrapConfig formatting = %q", rendered)
		}
	}
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
