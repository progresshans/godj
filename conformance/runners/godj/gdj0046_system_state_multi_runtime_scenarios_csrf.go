package godj

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type gdj0046CSRFAuthenticator struct{}

func (gdj0046CSRFAuthenticator) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return auth.Anonymous(), auth.ErrInvalidCredentials
}

func (gdj0046CSRFAuthenticator) Resolve(context.Context, string) (auth.Principal, error) {
	return auth.Anonymous(), auth.ErrInvalidCredentials
}

type gdj0046CSRFApplication struct {
	application *web.Application
}

type gdj0046IssuedCSRF struct {
	token  string
	cookie http.Cookie
}

func newGDJ0046CSRFApplication(ring websessionauth.CSRFKeyRing) (*gdj0046CSRFApplication, error) {
	return newGDJ0046CSRFApplicationWithManager(ring, nil)
}

func newGDJ0046CSRFApplicationWithManager(
	ring websessionauth.CSRFKeyRing,
	manager *sessions.Manager,
) (*gdj0046CSRFApplication, error) {
	if manager == nil {
		store, err := sessions.NewMemoryStore(4)
		if err != nil {
			return nil, err
		}
		manager, err = sessions.NewManager(store, sessions.Config{})
		if err != nil {
			return nil, err
		}
	}
	runtime, err := websessionauth.New(websessionauth.Config{
		Sessions:      manager,
		Authenticator: gdj0046CSRFAuthenticator{},
		Authorizer:    auth.PrincipalAuthorizer{},
		SessionCookie: websessionauth.CookieConfig{AllowInsecure: true},
		CSRFCookie:    websessionauth.CookieConfig{AllowInsecure: true},
		Random:        rand.Reader,
		CSRFKeyRing:   ring,
	})
	if err != nil {
		return nil, err
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "gdj0046_csrf_product",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/conformance/runners/godj/gdj0046csrf",
			Label: "gdj0046csrf",
		}},
	})
	if err != nil {
		return nil, err
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
			if errors.Is(err, &websessionauth.Error{Code: websessionauth.CodeCSRFRejected}) {
				return web.NewResponse(http.StatusForbidden, make(http.Header), []byte("Forbidden\n"))
			}
			return web.Response{}, err
		}
		return web.NewResponse(http.StatusNoContent, make(http.Header), nil)
	}
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{
			{Name: "gdj0046csrf:issue", Method: http.MethodGet, Path: "/issue/", Handler: issue},
			{Name: "gdj0046csrf:verify", Method: http.MethodPost, Path: "/verify/", Handler: verify},
		},
	})
	if err != nil {
		return nil, err
	}
	return &gdj0046CSRFApplication{application: application}, nil
}

func (application *gdj0046CSRFApplication) issue(ctx context.Context) (gdj0046IssuedCSRF, error) {
	request := httptest.NewRequest(http.MethodGet, "http://gdj0046.test/issue/", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	application.application.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return gdj0046IssuedCSRF{}, err
	}
	if response.StatusCode != http.StatusOK || len(body) != 128 {
		return gdj0046IssuedCSRF{}, fmt.Errorf(
			"issue CSRF response = status %d token bytes %d",
			response.StatusCode,
			len(body),
		)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == websessionauth.DefaultCSRFCookieName {
			return gdj0046IssuedCSRF{token: string(body), cookie: *cookie}, nil
		}
	}
	return gdj0046IssuedCSRF{}, errors.New("issue CSRF response omitted the CSRF cookie")
}

func (application *gdj0046CSRFApplication) verify(
	ctx context.Context,
	issued gdj0046IssuedCSRF,
) (int, error) {
	request := httptest.NewRequest(http.MethodPost, "http://gdj0046.test/verify/", nil).WithContext(ctx)
	request.AddCookie(&issued.cookie)
	request.Header.Set(websessionauth.DefaultCSRFHeader, issued.token)
	recorder := httptest.NewRecorder()
	application.application.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return 0, err
	}
	return response.StatusCode, nil
}

func gdj0046CSRFKey(value byte) []byte {
	return bytes.Repeat([]byte{value}, 32)
}

func gdj0046CSRFKeyRing(
	active []byte,
	validation ...[]byte,
) (websessionauth.CSRFKeyRing, error) {
	return websessionauth.NewCSRFKeyRing(active, validation...)
}

type gdj0046CSRFProviderCanary struct {
	calls      atomic.Int64
	active     []byte
	validation [][]byte
	state      string
}

func (provider *gdj0046CSRFProviderCanary) snapshot() (websessionauth.CSRFKeyRing, error) {
	provider.calls.Add(1)
	return gdj0046CSRFKeyRing(provider.active, provider.validation...)
}

func gdj0046VerifyStatus(
	ctx context.Context,
	application *gdj0046CSRFApplication,
	issued gdj0046IssuedCSRF,
	want int,
) (bool, error) {
	status, err := application.verify(ctx, issued)
	if err != nil {
		return false, err
	}
	return status == want, nil
}

func systemStateSharedCSRFKeyRing(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	sharedKey := gdj0046CSRFKey('S')
	oldKey := gdj0046CSRFKey('O')
	newKey := gdj0046CSRFKey('N')
	unrelatedKey := gdj0046CSRFKey('U')
	sharedRing, err := gdj0046CSRFKeyRing(sharedKey)
	if err != nil {
		return protocol.Observation{}, err
	}
	sharedA, err := newGDJ0046CSRFApplication(sharedRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	sharedB, err := newGDJ0046CSRFApplication(sharedRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	sharedToken, err := sharedA.issue(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	crossRuntimeAccepted, err := gdj0046VerifyStatus(ctx, sharedB, sharedToken, http.StatusNoContent)
	if err != nil {
		return protocol.Observation{}, err
	}

	oldStagedRing, err := gdj0046CSRFKeyRing(oldKey, newKey)
	if err != nil {
		return protocol.Observation{}, err
	}
	newStagedRing, err := gdj0046CSRFKeyRing(newKey, oldKey)
	if err != nil {
		return protocol.Observation{}, err
	}
	oldOnlyRing, err := gdj0046CSRFKeyRing(oldKey)
	if err != nil {
		return protocol.Observation{}, err
	}
	newOnlyRing, err := gdj0046CSRFKeyRing(newKey)
	if err != nil {
		return protocol.Observation{}, err
	}
	unrelatedRing, err := gdj0046CSRFKeyRing(unrelatedKey)
	if err != nil {
		return protocol.Observation{}, err
	}
	oldStaged, err := newGDJ0046CSRFApplication(oldStagedRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	newStaged, err := newGDJ0046CSRFApplication(newStagedRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	oldOnly, err := newGDJ0046CSRFApplication(oldOnlyRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	newOnly, err := newGDJ0046CSRFApplication(newOnlyRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	unrelated, err := newGDJ0046CSRFApplication(unrelatedRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	oldToken, err := oldStaged.issue(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	newToken, err := newStaged.issue(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	oldAcceptedByNew, err := gdj0046VerifyStatus(ctx, newStaged, oldToken, http.StatusNoContent)
	if err != nil {
		return protocol.Observation{}, err
	}
	newAcceptedByOld, err := gdj0046VerifyStatus(ctx, oldStaged, newToken, http.StatusNoContent)
	if err != nil {
		return protocol.Observation{}, err
	}
	oldAcceptedByOld, err := gdj0046VerifyStatus(ctx, oldOnly, oldToken, http.StatusNoContent)
	if err != nil {
		return protocol.Observation{}, err
	}
	newAcceptedByNew, err := gdj0046VerifyStatus(ctx, newOnly, newToken, http.StatusNoContent)
	if err != nil {
		return protocol.Observation{}, err
	}
	removedRejected, err := gdj0046VerifyStatus(ctx, newOnly, oldToken, http.StatusForbidden)
	if err != nil {
		return protocol.Observation{}, err
	}
	unrelatedRejected, err := gdj0046VerifyStatus(ctx, unrelated, oldToken, http.StatusForbidden)
	if err != nil {
		return protocol.Observation{}, err
	}
	newRejectedByOldOnly, err := gdj0046VerifyStatus(ctx, oldOnly, newToken, http.StatusForbidden)
	if err != nil {
		return protocol.Observation{}, err
	}
	stagedRotation := oldAcceptedByNew && newAcceptedByOld && oldAcceptedByOld && newAcceptedByNew
	activeSigns := newAcceptedByNew && newRejectedByOldOnly && oldAcceptedByOld && removedRejected

	// Constructor copying is proved by mutating the caller slices after ring
	// construction and verifying a token with a ring built from snapshots.
	aliasActive := gdj0046CSRFKey('A')
	aliasValidation := gdj0046CSRFKey('V')
	activeSnapshot := append([]byte(nil), aliasActive...)
	validationSnapshot := append([]byte(nil), aliasValidation...)
	aliasRing, err := gdj0046CSRFKeyRing(aliasActive, aliasValidation)
	if err != nil {
		return protocol.Observation{}, err
	}
	for index := range aliasActive {
		aliasActive[index] = 'X'
		aliasValidation[index] = 'Y'
	}
	snapshotRing, err := gdj0046CSRFKeyRing(activeSnapshot, validationSnapshot)
	if err != nil {
		return protocol.Observation{}, err
	}
	aliasApplication, err := newGDJ0046CSRFApplication(aliasRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	snapshotApplication, err := newGDJ0046CSRFApplication(snapshotRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	aliasToken, err := aliasApplication.issue(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	activeCopyPreserved, err := gdj0046VerifyStatus(ctx, snapshotApplication, aliasToken, http.StatusNoContent)
	if err != nil {
		return protocol.Observation{}, err
	}
	validationSignerRing, err := gdj0046CSRFKeyRing(validationSnapshot)
	if err != nil {
		return protocol.Observation{}, err
	}
	validationSigner, err := newGDJ0046CSRFApplication(validationSignerRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	validationToken, err := validationSigner.issue(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	validationCopyPreserved, err := gdj0046VerifyStatus(ctx, aliasApplication, validationToken, http.StatusNoContent)
	if err != nil {
		return protocol.Observation{}, err
	}
	aliasCopyPreserved := activeCopyPreserved && validationCopyPreserved

	// Composition, not the framework runtime, owns provider lifecycle. Load one
	// immutable key-ring snapshot through an instrumented caller-owned provider,
	// then use it across two durable system-state runtimes after mutating every
	// provider-owned byte and its state marker.
	provider := &gdj0046CSRFProviderCanary{
		active:     gdj0046CSRFKey('P'),
		validation: [][]byte{gdj0046CSRFKey('Q')},
		state:      "gdj0046-csrf-provider-state-canary",
	}
	providerActiveSnapshot := append([]byte(nil), provider.active...)
	providerValidationSnapshot := append([]byte(nil), provider.validation[0]...)
	providerStateMarker := provider.state
	providerRing, err := provider.snapshot()
	if err != nil {
		return protocol.Observation{}, err
	}
	durablePair, err := newGDJ0046RuntimePair(ctx, 4, 4, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer durablePair.cleanup()
	holderManager, err := sessions.NewManager(durablePair.holder.SessionStore(), sessions.Config{})
	if err != nil {
		return protocol.Observation{}, err
	}
	contenderManager, err := sessions.NewManager(durablePair.contender.SessionStore(), sessions.Config{})
	if err != nil {
		return protocol.Observation{}, err
	}
	providerA, err := newGDJ0046CSRFApplicationWithManager(providerRing, holderManager)
	if err != nil {
		return protocol.Observation{}, err
	}
	providerB, err := newGDJ0046CSRFApplicationWithManager(providerRing, contenderManager)
	if err != nil {
		return protocol.Observation{}, err
	}
	for index := range provider.active {
		provider.active[index] = 'X'
	}
	for index := range provider.validation[0] {
		provider.validation[0][index] = 'Y'
	}
	provider.state = "mutated-by-caller"
	providerToken, err := providerA.issue(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	providerHandoff, err := gdj0046VerifyStatus(ctx, providerB, providerToken, http.StatusNoContent)
	if err != nil {
		return protocol.Observation{}, err
	}
	providerCallsStable := provider.calls.Load() == 1

	maximumValidation := make([][]byte, 7)
	for index := range maximumValidation {
		maximumValidation[index] = gdj0046CSRFKey(byte('a' + index))
	}
	boundedActive := gdj0046CSRFKey('Z')
	overLimitKey := gdj0046CSRFKey('z')
	boundedRing, boundedErr := gdj0046CSRFKeyRing(boundedActive, maximumValidation...)
	overLimitValidation := append(append([][]byte(nil), maximumValidation...), overLimitKey)
	_, overLimitErr := gdj0046CSRFKeyRing(boundedActive, overLimitValidation...)
	verificationBounded := boundedErr == nil && boundedRing.Valid() &&
		errors.Is(overLimitErr, &websessionauth.Error{Code: websessionauth.CodeInvalidConfig, Field: "csrf_key_ring"})
	unboundedPaths := 0
	if !verificationBounded {
		unboundedPaths = 1
	}
	if !crossRuntimeAccepted || !stagedRotation || !activeSigns || !removedRejected ||
		!unrelatedRejected || !aliasCopyPreserved || !providerHandoff ||
		!providerCallsStable || !verificationBounded {
		return protocol.Observation{}, fmt.Errorf(
			"CSRF ring facts drifted: cross=%v staged=%v active=%v removed=%v unrelated=%v copy=%v provider=%v/%d bounded=%v",
			crossRuntimeAccepted,
			stagedRotation,
			activeSigns,
			removedRejected,
			unrelatedRejected,
			aliasCopyPreserved,
			providerHandoff,
			provider.calls.Load(),
			verificationBounded,
		)
	}

	result := protocol.Object(map[string]protocol.Value{
		"active_key_signs_new_values": protocol.Boolean(activeSigns),
		"cross_runtime_handoff":       protocol.String("accepted"),
		"removed_key":                 protocol.String("rejected"),
		"staged_rotation":             protocol.String("old_and_new_accepted"),
		"unrelated_key":               protocol.String("rejected"),
	})
	ringMutable := !aliasCopyPreserved
	ringDiagnostics, err := json.Marshal(sharedRing)
	if err != nil {
		return protocol.Observation{}, err
	}
	configDiagnostics, configDiagnosticErr := json.Marshal(websessionauth.Config{CSRFKeyRing: sharedRing})
	const redactedRingDiagnostic = "sessionauth.CSRFKeyRing{redacted}"
	if string(ringDiagnostics) != `"`+redactedRingDiagnostic+`"` {
		return protocol.Observation{}, errors.New("CSRF ring JSON diagnostic contract drifted")
	}
	if configDiagnosticErr == nil {
		var configDiagnosticFields map[string]json.RawMessage
		if err := json.Unmarshal(configDiagnostics, &configDiagnosticFields); err != nil {
			return protocol.Observation{}, fmt.Errorf("decode CSRF config diagnostic: %w", err)
		}
		if _, exposed := configDiagnosticFields["CSRFKeyRing"]; exposed {
			return protocol.Observation{}, errors.New("CSRF config JSON diagnostic exposed the key-ring field")
		}
	}
	ringFormatted := []string{
		fmt.Sprintf("%v", sharedRing),
		fmt.Sprintf("%#v", sharedRing),
		fmt.Sprintf("%d", sharedRing),
	}
	for _, output := range ringFormatted {
		if output != redactedRingDiagnostic {
			return protocol.Observation{}, errors.New("CSRF ring diagnostic was not exactly redacted")
		}
	}
	configFormatted := []string{
		fmt.Sprintf("%v", websessionauth.Config{CSRFKeyRing: sharedRing}),
		fmt.Sprintf("%#v", websessionauth.Config{CSRFKeyRing: sharedRing}),
	}
	for _, output := range configFormatted {
		if !strings.Contains(output, redactedRingDiagnostic) ||
			strings.Contains(output, "keys:") || strings.Contains(output, "count:") {
			return protocol.Observation{}, errors.New("CSRF config diagnostic bypassed key-ring redaction")
		}
	}
	diagnosticValues := []string{
		string(ringDiagnostics),
		string(configDiagnostics),
		fmt.Sprint(configDiagnosticErr),
		fmt.Sprint(boundedErr),
		fmt.Sprint(overLimitErr),
		fmt.Sprintf("%#v", overLimitErr),
	}
	diagnosticValues = append(diagnosticValues, ringFormatted...)
	diagnosticValues = append(diagnosticValues, configFormatted...)
	keyMaterialVariants := make([]string, 0, 40)
	allKeyMaterial := [][]byte{
		sharedKey,
		oldKey,
		newKey,
		unrelatedKey,
		activeSnapshot,
		validationSnapshot,
		providerActiveSnapshot,
		providerValidationSnapshot,
		boundedActive,
		overLimitKey,
	}
	allKeyMaterial = append(allKeyMaterial, maximumValidation...)
	for _, secret := range allKeyMaterial {
		var fixed [32]byte
		copy(fixed[:], secret)
		jsonArray, err := json.Marshal(fixed)
		if err != nil {
			return protocol.Observation{}, err
		}
		keyMaterialVariants = append(
			keyMaterialVariants,
			string(secret),
			hex.EncodeToString(secret),
			base64.RawStdEncoding.EncodeToString(secret),
			base64.RawURLEncoding.EncodeToString(secret),
			fmt.Sprint(secret),
			fmt.Sprintf("%#v", secret),
			string(jsonArray),
		)
	}
	credentialSnapshot, credentialRows, err := gdj0046ReadCredentialSnapshot(ctx, durablePair.holder)
	if err != nil || credentialRows != 1 {
		return protocol.Observation{}, fmt.Errorf("read durable CSRF canary credential: rows=%d: %w", credentialRows, err)
	}
	databaseBytes, err := os.ReadFile(filepath.Join(durablePair.backends.directory, "system-state.sqlite3"))
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("read durable CSRF canary database: %w", err)
	}
	storageValues := append(credentialSnapshot.storageValues(), string(databaseBytes))
	keyMaterialOccurrences, err := systemStateSecretOccurrences(nil, storageValues, keyMaterialVariants...)
	if err != nil {
		return protocol.Observation{}, err
	}
	providerStateOccurrences, err := systemStateSecretOccurrences(
		nil,
		append(append([]string(nil), storageValues...), diagnosticValues...),
		providerStateMarker,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	keyMaterialPersisted := keyMaterialOccurrences != 0
	providerStateOwned := !providerCallsStable || !providerHandoff || providerStateOccurrences != 0
	dbState := protocol.Object(map[string]protocol.Value{
		"key_material_persisted":            protocol.Boolean(keyMaterialPersisted),
		"provider_state_owned_by_framework": protocol.Boolean(providerStateOwned),
		"ring_mutable":                      protocol.Boolean(ringMutable),
	})
	secretVariants := append([]string(nil), keyMaterialVariants...)
	secretVariants = append(secretVariants,
		sharedToken.token,
		sharedToken.cookie.Value,
		oldToken.token,
		oldToken.cookie.Value,
		newToken.token,
		newToken.cookie.Value,
		aliasToken.token,
		aliasToken.cookie.Value,
		validationToken.token,
		validationToken.cookie.Value,
		providerToken.token,
		providerToken.cookie.Value,
		providerStateMarker,
	)
	secretValuesSerialized, err := systemStateSecretOccurrences(
		[]protocol.Value{result, dbState},
		append(append([]string(nil), storageValues...), diagnosticValues...),
		secretVariants...,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	if keyMaterialPersisted || providerStateOwned || secretValuesSerialized != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"CSRF storage/diagnostic canary escaped: key=%d provider=%d calls=%d serialized=%d",
			keyMaterialOccurrences,
			providerStateOccurrences,
			provider.calls.Load(),
			secretValuesSerialized,
		)
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"secret_values_serialized":     systemStateInt64(secretValuesSerialized),
		"unbounded_verification_paths": systemStateInt(unboundedPaths),
		"verification_key_set_bounded": protocol.Boolean(verificationBounded),
	}))
}
