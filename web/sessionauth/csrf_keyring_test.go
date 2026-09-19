package sessionauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

func TestCSRFKeyRingCrossRuntimeAndStagedRotation(t *testing.T) {
	shared := repeatedCSRFKey(0x11)
	sharedRing := mustCSRFKeyRing(t, shared)
	runtimeA := newCSRFKeyRuntime(t, sharedRing, nil)
	runtimeB := newCSRFKeyRuntime(t, sharedRing, nil)
	applicationA := newCSRFKeyApplication(t, runtimeA)
	applicationB := newCSRFKeyApplication(t, runtimeB)

	issuedByA := issueCSRFKeyToken(t, applicationA)
	verifyCSRFKeyToken(t, applicationB, issuedByA, http.StatusNoContent)
	issuedByB := issueCSRFKeyToken(t, applicationB)
	verifyCSRFKeyToken(t, applicationA, issuedByB, http.StatusNoContent)

	oldKey := repeatedCSRFKey(0x21)
	newKey := repeatedCSRFKey(0x31)
	unrelatedKey := repeatedCSRFKey(0x41)
	oldStaged := newCSRFKeyApplication(t, newCSRFKeyRuntime(t, mustCSRFKeyRing(t, oldKey, newKey), nil))
	newStaged := newCSRFKeyApplication(t, newCSRFKeyRuntime(t, mustCSRFKeyRing(t, newKey, oldKey), nil))
	oldOnly := newCSRFKeyApplication(t, newCSRFKeyRuntime(t, mustCSRFKeyRing(t, oldKey), nil))
	newOnly := newCSRFKeyApplication(t, newCSRFKeyRuntime(t, mustCSRFKeyRing(t, newKey), nil))
	unrelated := newCSRFKeyApplication(t, newCSRFKeyRuntime(t, mustCSRFKeyRing(t, unrelatedKey), nil))

	oldToken := issueCSRFKeyToken(t, oldStaged)
	verifyCSRFKeyToken(t, newStaged, oldToken, http.StatusNoContent)
	verifyCSRFKeyToken(t, oldOnly, oldToken, http.StatusNoContent)
	// The validation-only new key must not sign tokens from the old Runtime;
	// after the old key is removed, that old token is rejected.
	verifyCSRFKeyToken(t, newOnly, oldToken, http.StatusForbidden)
	verifyCSRFKeyToken(t, unrelated, oldToken, http.StatusForbidden)

	newToken := issueCSRFKeyToken(t, newStaged)
	verifyCSRFKeyToken(t, oldStaged, newToken, http.StatusNoContent)
	verifyCSRFKeyToken(t, newOnly, newToken, http.StatusNoContent)
	// Removing the old key after the deployment window rejects old material.
	verifyCSRFKeyToken(t, oldOnly, newToken, http.StatusForbidden)
}

func TestCSRFKeyRingConstructorBoundsAliasingAndRedaction(t *testing.T) {
	active := []byte("csrf-active-material-marker-0001")
	validation := []byte("csrf-validate-material-marker-01")
	if len(active) != csrfSecretBytes || len(validation) != csrfSecretBytes {
		t.Fatal("test key fixture is not exactly 32 bytes")
	}
	activeSnapshot := append([]byte(nil), active...)
	validationSnapshot := append([]byte(nil), validation...)
	ring, err := NewCSRFKeyRing(active, validation)
	if err != nil {
		t.Fatal(err)
	}
	if !ring.Valid() {
		t.Fatal("constructor returned an invalid key ring")
	}
	for index := range active {
		active[index] ^= 0xff
		validation[index] ^= 0xff
	}
	message := []byte("masked-csrf-prefix")
	activeMAC := csrfMACForKey(bytesToCSRFKey(t, activeSnapshot), message)
	validationMAC := csrfMACForKey(bytesToCSRFKey(t, validationSnapshot), message)
	if ring.verify(message, activeMAC[:]) != 1 || ring.verify(message, validationMAC[:]) != 1 {
		t.Fatal("caller mutation changed copied key material")
	}

	encoded, err := json.Marshal(ring)
	if err != nil {
		t.Fatal(err)
	}
	rendered := []string{
		fmt.Sprint(ring),
		fmt.Sprintf("%+v", ring),
		fmt.Sprintf("%#v", ring),
		fmt.Sprint(&ring),
		fmt.Sprintf("%#v", &ring),
		string(encoded),
	}
	for _, format := range []string{"%b", "%c", "%d", "%e", "%f", "%g", "%o", "%U", "%x", "%X"} {
		rendered = append(rendered, fmt.Sprintf(format, ring), fmt.Sprintf(format, &ring))
	}
	for _, output := range rendered {
		if output != "sessionauth.CSRFKeyRing{redacted}" && output != `"sessionauth.CSRFKeyRing{redacted}"` {
			t.Fatalf("unexpected key-ring diagnostic: %q", output)
		}
		for _, secret := range [][]byte{activeSnapshot, validationSnapshot} {
			if strings.Contains(output, string(secret)) ||
				strings.Contains(output, hex.EncodeToString(secret)) ||
				strings.Contains(output, base64.RawStdEncoding.EncodeToString(secret)) {
				t.Fatalf("key-ring diagnostic exposed material: %q", output)
			}
		}
	}
	// fmt handles %p and %w before Formatter. The public value therefore retains
	// only a pointer to private immutable state, so malformed uses remain
	// address-only diagnostics rather than recursive key-array formatting.
	wrapFormat := strings.Join([]string{"%", "w"}, "")
	for _, output := range []string{
		fmt.Sprintf("%p", ring),
		fmt.Sprintf("%p", &ring),
		fmt.Sprintf(wrapFormat, ring),
		fmt.Sprintf(wrapFormat, &ring),
		fmt.Errorf(wrapFormat, ring).Error(),
		fmt.Errorf(wrapFormat, &ring).Error(),
	} {
		for _, secret := range [][]byte{activeSnapshot, validationSnapshot} {
			decimal := strings.Trim(fmt.Sprint(secret), "[]")
			if strings.Contains(output, string(secret)) ||
				strings.Contains(output, hex.EncodeToString(secret)) ||
				strings.Contains(output, base64.RawStdEncoding.EncodeToString(secret)) ||
				strings.Contains(output, decimal) {
				t.Fatalf("pointer diagnostic exposed key material: %q", output)
			}
		}
	}
	for _, output := range []string{
		fmt.Sprintf("%+v", Config{CSRFKeyRing: ring}),
		fmt.Sprintf("%#v", Config{CSRFKeyRing: ring}),
	} {
		if !strings.Contains(output, "sessionauth.CSRFKeyRing{redacted}") ||
			strings.Contains(output, "keys:") || strings.Contains(output, "count:") {
			t.Fatalf("Config diagnostic bypassed key-ring redaction: %q", output)
		}
		for _, secret := range [][]byte{activeSnapshot, validationSnapshot} {
			if strings.Contains(output, string(secret)) ||
				strings.Contains(output, hex.EncodeToString(secret)) ||
				strings.Contains(output, base64.RawStdEncoding.EncodeToString(secret)) {
				t.Fatalf("Config diagnostic exposed key material: %q", output)
			}
		}
	}
	field, found := reflect.TypeOf(Config{}).FieldByName("CSRFKeyRing")
	if !found || field.Tag.Get("json") != "-" {
		t.Fatalf("Config.CSRFKeyRing JSON tag = %q, want -", field.Tag.Get("json"))
	}

	for name, constructor := range map[string]func() error{
		"missing active": func() error {
			_, err := NewCSRFKeyRing(nil)
			return err
		},
		"short active": func() error {
			_, err := NewCSRFKeyRing(activeSnapshot[:csrfSecretBytes-1])
			return err
		},
		"long active": func() error {
			_, err := NewCSRFKeyRing(append(activeSnapshot, 0))
			return err
		},
		"short validation": func() error {
			_, err := NewCSRFKeyRing(activeSnapshot, validationSnapshot[:csrfSecretBytes-1])
			return err
		},
		"duplicate active": func() error {
			_, err := NewCSRFKeyRing(activeSnapshot, activeSnapshot)
			return err
		},
		"duplicate validation": func() error {
			_, err := NewCSRFKeyRing(activeSnapshot, validationSnapshot, validationSnapshot)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := constructor()
			if !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "csrf_key_ring"}) {
				t.Fatalf("NewCSRFKeyRing error = %v", err)
			}
			for _, marker := range []string{string(activeSnapshot), string(validationSnapshot)} {
				if strings.Contains(err.Error(), marker) || strings.Contains(fmt.Sprintf("%#v", err), marker) {
					t.Fatalf("constructor error exposed key material: %v", err)
				}
			}
		})
	}

	validationKeys := make([][]byte, maxCSRFVerificationKeys-1)
	for index := range validationKeys {
		validationKeys[index] = repeatedCSRFKey(byte(index + 2))
	}
	if bounded, err := NewCSRFKeyRing(repeatedCSRFKey(1), validationKeys...); err != nil || !bounded.Valid() {
		t.Fatalf("maximum bounded key ring rejected: valid=%v err=%v", bounded.Valid(), err)
	}
	validationKeys = append(validationKeys, repeatedCSRFKey(0xfe))
	if _, err := NewCSRFKeyRing(repeatedCSRFKey(1), validationKeys...); !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "csrf_key_ring"}) {
		t.Fatalf("over-limit key ring error = %v", err)
	}
	if (CSRFKeyRing{}).Valid() {
		t.Fatal("zero key ring reported valid")
	}
}

func TestCSRFKeyRingVerificationTraversesEveryConfiguredKey(t *testing.T) {
	keys := make([][csrfSecretBytes]byte, maxCSRFVerificationKeys)
	for index := range keys {
		keys[index][0] = byte(index + 1)
	}
	candidate := [32]byte{0xa5}
	visited := make([]byte, 0, len(keys))
	compute := func(key [csrfSecretBytes]byte, _ []byte) [32]byte {
		visited = append(visited, key[0])
		if key[0] == keys[0][0] {
			return candidate
		}
		return [32]byte{key[0]}
	}
	if matched := csrfMACMatches(keys, []byte("message"), candidate[:], compute); matched != 1 {
		t.Fatalf("csrfMACMatches = %d, want 1", matched)
	}
	if len(visited) != len(keys) {
		t.Fatalf("verification visited %d/%d keys after an early match", len(visited), len(keys))
	}
	for index, key := range keys {
		if visited[index] != key[0] {
			t.Fatalf("verification order[%d] = %d, want %d", index, visited[index], key[0])
		}
	}
}

func TestCSRFKeyRingZeroValuePreservesProcessLocalEntropyAndRejection(t *testing.T) {
	zeroEntropy := &countingCSRFReader{value: 0x51}
	zeroRuntime := newCSRFKeyRuntime(t, CSRFKeyRing{}, zeroEntropy)
	if zeroEntropy.BytesRead() != csrfSecretBytes {
		t.Fatalf("zero-ring New consumed %d bytes, want %d", zeroEntropy.BytesRead(), csrfSecretBytes)
	}
	explicitEntropy := &countingCSRFReader{value: 0x61}
	explicitRing := mustCSRFKeyRing(t, repeatedCSRFKey(0x71))
	_ = newCSRFKeyRuntime(t, explicitRing, explicitEntropy)
	if explicitEntropy.BytesRead() != 0 {
		t.Fatalf("explicit-ring New consumed %d runtime entropy bytes", explicitEntropy.BytesRead())
	}

	first := newCSRFKeyApplication(t, zeroRuntime)
	secondEntropy := &countingCSRFReader{value: 0x52}
	second := newCSRFKeyApplication(t, newCSRFKeyRuntime(t, CSRFKeyRing{}, secondEntropy))
	issued := issueCSRFKeyToken(t, first)
	if len(issued.token) != csrfEncodedTokenSize || len(issued.cookie.Value) != csrfEncodedSecretSize {
		t.Fatalf("zero-ring bytes drifted: token=%d cookie=%d", len(issued.token), len(issued.cookie.Value))
	}
	verifyCSRFKeyToken(t, first, issued, http.StatusNoContent)
	verifyCSRFKeyToken(t, second, issued, http.StatusForbidden)
}

func TestCSRFKeyRingInvalidInternalStateFailsConfigWithoutMaterial(t *testing.T) {
	config := csrfKeyRuntimeConfig(t, CSRFKeyRing{}, nil)
	invalid := CSRFKeyRing{state: &csrfKeyRingState{count: maxCSRFVerificationKeys + 1}}
	copy(invalid.state.keys[0][:], []byte("csrf-config-secret-marker-00001"))
	config.CSRFKeyRing = invalid
	_, err := New(config)
	if !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "csrf_key_ring"}) {
		t.Fatalf("New invalid ring error = %v", err)
	}
	for _, output := range []string{err.Error(), fmt.Sprintf("%#v", err)} {
		if strings.Contains(output, "csrf-config-secret-marker") {
			t.Fatalf("configuration error exposed key material: %q", output)
		}
	}
}

type issuedCSRFKeyToken struct {
	token  string
	cookie http.Cookie
}

func issueCSRFKeyToken(t *testing.T, application *web.Application) issuedCSRFKeyToken {
	t.Helper()
	recorder := httptest.NewRecorder()
	application.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://example.test/issue/", nil))
	result := recorder.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusOK || len(body) != csrfEncodedTokenSize {
		t.Fatalf("issue response status=%d token-bytes=%d", result.StatusCode, len(body))
	}
	for _, cookie := range result.Cookies() {
		if cookie.Name == DefaultCSRFCookieName {
			return issuedCSRFKeyToken{token: string(body), cookie: *cookie}
		}
	}
	t.Fatal("issue response omitted CSRF cookie")
	return issuedCSRFKeyToken{}
}

func verifyCSRFKeyToken(t *testing.T, application *web.Application, issued issuedCSRFKeyToken, want int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://example.test/verify/", nil)
	request.AddCookie(&issued.cookie)
	request.Header.Set(DefaultCSRFHeader, issued.token)
	recorder := httptest.NewRecorder()
	application.ServeHTTP(recorder, request)
	if recorder.Code != want {
		t.Fatalf("verify status=%d, want %d", recorder.Code, want)
	}
}

func newCSRFKeyApplication(t *testing.T, runtime *Runtime) *web.Application {
	t.Helper()
	configured, err := settings.New(settings.Definition{
		ProjectName: "csrf_key_ring_test",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/web/sessionauth/csrfkeyringtest",
			Label: "csrfkeys",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	issue := func(request *web.Request) (web.Response, error) {
		token, err := runtime.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := web.NewResponse(http.StatusOK, make(http.Header), []byte(token.Value()))
		if err != nil {
			return web.Response{}, err
		}
		return token.Apply(response)
	}
	verify := func(request *web.Request) (web.Response, error) {
		if err := runtime.VerifyCSRF(request, nil); err != nil {
			if errors.Is(err, &Error{Code: CodeCSRFRejected}) {
				return web.NewResponse(http.StatusForbidden, make(http.Header), []byte("Forbidden\n"))
			}
			return web.Response{}, err
		}
		return web.NewResponse(http.StatusNoContent, make(http.Header), nil)
	}
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{
			{Name: "csrfkeys:issue", Method: http.MethodGet, Path: "/issue/", Handler: issue},
			{Name: "csrfkeys:verify", Method: http.MethodPost, Path: "/verify/", Handler: verify},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func newCSRFKeyRuntime(t *testing.T, ring CSRFKeyRing, random io.Reader) *Runtime {
	t.Helper()
	runtime, err := New(csrfKeyRuntimeConfig(t, ring, random))
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func csrfKeyRuntimeConfig(t *testing.T, ring CSRFKeyRing, random io.Reader) Config {
	t.Helper()
	store, err := sessions.NewMemoryStore(4)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Sessions:      manager,
		Authenticator: csrfKeyTestAuthenticator{},
		Authorizer:    auth.PrincipalAuthorizer{},
		SessionCookie: CookieConfig{AllowInsecure: true},
		CSRFCookie:    CookieConfig{AllowInsecure: true},
		Random:        random,
		CSRFKeyRing:   ring,
	}
}

type csrfKeyTestAuthenticator struct{}

func (csrfKeyTestAuthenticator) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return auth.Anonymous(), auth.ErrInvalidCredentials
}

func (csrfKeyTestAuthenticator) Resolve(context.Context, string) (auth.Principal, error) {
	return auth.Anonymous(), auth.ErrInvalidCredentials
}

type countingCSRFReader struct {
	value byte
	read  int
}

func (reader *countingCSRFReader) Read(target []byte) (int, error) {
	for index := range target {
		target[index] = reader.value
	}
	reader.read += len(target)
	return len(target), nil
}

func (reader *countingCSRFReader) BytesRead() int { return reader.read }

func repeatedCSRFKey(value byte) []byte {
	return bytes.Repeat([]byte{value}, csrfSecretBytes)
}

func mustCSRFKeyRing(t *testing.T, active []byte, validation ...[]byte) CSRFKeyRing {
	t.Helper()
	ring, err := NewCSRFKeyRing(active, validation...)
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func bytesToCSRFKey(t *testing.T, material []byte) [csrfSecretBytes]byte {
	t.Helper()
	if len(material) != csrfSecretBytes {
		t.Fatalf("key material length=%d", len(material))
	}
	var key [csrfSecretBytes]byte
	copy(key[:], material)
	return key
}
