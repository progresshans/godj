package bearerauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const (
	// MaxTokenBytes is the fixed upper bound for the credential portion of one
	// Authorization field. It is intentionally not configurable in this profile.
	MaxTokenBytes = 4096

	challengeBearer            = "Bearer"
	challengeInvalidRequest    = `Bearer error="invalid_request"`
	challengeInvalidToken      = `Bearer error="invalid_token"`
	challengeInsufficientScope = `Bearer error="insufficient_scope"`
)

// Verifier resolves one syntactically valid opaque token into an active
// principal. Unknown, expired, revoked, and inactive tokens must return
// auth.ErrInvalidCredentials. Other failures retain infrastructure ownership.
type Verifier interface {
	Verify(context.Context, Token) (auth.Principal, error)
}

// Config is immutable startup input for one Bearer authentication profile.
// Both collaborators are required; their concrete values are never formatted
// or serialized by this type.
type Config struct {
	Verifier   Verifier        `json:"-"`
	Authorizer auth.Authorizer `json:"-"`
}

func (Config) String() string   { return "bearerauth.Config{redacted}" }
func (Config) GoString() string { return "bearerauth.Config{redacted}" }
func (Config) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "bearerauth.Config{redacted}")
}

// Runtime is an immutable API Bearer adapter. Its zero value is invalid.
type Runtime struct {
	verifier   Verifier
	authorizer auth.Authorizer
}

func (Runtime) String() string   { return "bearerauth.Runtime{redacted}" }
func (Runtime) GoString() string { return "bearerauth.Runtime{redacted}" }
func (Runtime) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "bearerauth.Runtime{redacted}")
}

var _ api.Authentication = (*Runtime)(nil)

// New validates the complete Bearer profile before any route can be wrapped.
func New(config Config) (*Runtime, error) {
	if nilInterface(config.Verifier) {
		return nil, &Error{Code: CodeInvalidConfig, Field: "verifier", Detail: "Bearer verifier is nil"}
	}
	if nilInterface(config.Authorizer) {
		return nil, &Error{Code: CodeInvalidConfig, Field: "authorizer", Detail: "authorizer is nil"}
	}
	return &Runtime{verifier: config.Verifier, authorizer: config.Authorizer}, nil
}

// Require constructs one protected handler. Credential parsing, verification,
// deny-overlay authorization, and the application handler each run at most once
// per request. Bearer requests never consult cookies, query/form values, or CSRF.
func (r *Runtime) Require(permission auth.Permission, handler api.AuthenticatedHandler) (web.Handler, error) {
	if r == nil || nilInterface(r.verifier) || nilInterface(r.authorizer) {
		return nil, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "Bearer runtime is nil or uninitialized"}
	}
	canonical, err := auth.NewPermission(string(permission))
	if err != nil || canonical != permission {
		return nil, &Error{Code: CodeInvalidConfig, Field: "permission", Detail: "permission is invalid"}
	}
	if handler == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "handler", Detail: "authenticated API handler is nil"}
	}

	return func(request *web.Request) (web.Response, error) {
		if request == nil || request.HTTP() == nil {
			return web.Response{}, &Error{Code: CodeInvalidRequest, Field: "request", Detail: "request is nil or outside its borrowed lifetime"}
		}
		ctx := request.Context()
		if err := preservedContextError(ctx, nil); err != nil {
			return web.Response{}, err
		}
		principal, outcome, err := r.resolve(ctx, request.HTTP().Header)
		if err != nil {
			return web.Response{}, err
		}
		switch outcome {
		case bearerMissing, bearerUnsupported:
			return denialResponse(http.StatusUnauthorized, api.CodeNotAuthenticated, challengeBearer)
		case bearerMalformed:
			return denialResponse(http.StatusBadRequest, api.CodeNotAuthenticated, challengeInvalidRequest)
		case bearerInvalid:
			return denialResponse(http.StatusUnauthorized, api.CodeNotAuthenticated, challengeInvalidToken)
		case bearerAccepted:
			// Continue below.
		default:
			return web.Response{}, &Error{Code: CodeInvalidRequest, Field: "authorization", Detail: "Bearer parser returned an invalid state"}
		}

		allowed, err := r.allowed(ctx, principal, permission)
		if err != nil {
			return web.Response{}, err
		}
		if !allowed {
			return denialResponse(http.StatusForbidden, api.CodePermissionDenied, challengeInsufficientScope)
		}
		return handler(request, principal)
	}, nil
}

func (r *Runtime) allowed(ctx context.Context, principal auth.Principal, permission auth.Permission) (bool, error) {
	if err := preservedContextError(ctx, nil); err != nil {
		return false, err
	}
	if !principal.Has(permission) {
		return false, nil
	}
	allowed, err := r.authorizer.Allowed(ctx, principal, permission)
	if contextErr := preservedContextError(ctx, err); contextErr != nil {
		return false, contextErr
	}
	if err != nil {
		return false, &Error{Code: CodeAuthorization, Detail: "permission evaluation failed", Cause: err}
	}
	return allowed, nil
}

type bearerOutcome uint8

const (
	bearerMissing bearerOutcome = iota
	bearerUnsupported
	bearerMalformed
	bearerInvalid
	bearerAccepted
)

func (r *Runtime) resolve(ctx context.Context, header http.Header) (auth.Principal, bearerOutcome, error) {
	if err := preservedContextError(ctx, nil); err != nil {
		return auth.Principal{}, bearerInvalid, err
	}
	token, outcome := parseAuthorization(header)
	if outcome != bearerAccepted {
		return auth.Principal{}, outcome, nil
	}
	defer token.release()
	principal, err := r.verifier.Verify(ctx, token)
	if contextErr := preservedContextError(ctx, err); contextErr != nil {
		return auth.Principal{}, bearerInvalid, contextErr
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		return auth.Principal{}, bearerInvalid, nil
	}
	if err != nil {
		return auth.Principal{}, bearerInvalid, &Error{Code: CodeVerification, Detail: "Bearer verification failed", Cause: err}
	}
	if !principal.Authenticated() {
		return auth.Principal{}, bearerInvalid, nil
	}
	return principal, bearerAccepted, nil
}

// parseAuthorization reads the raw map so differently-cased duplicate keys
// cannot be hidden by http.Header.Get or Values canonicalization.
func parseAuthorization(header http.Header) (Token, bearerOutcome) {
	found := false
	var value string
	for name, values := range header {
		if !strings.EqualFold(name, "Authorization") {
			continue
		}
		if found || len(values) != 1 {
			return Token{}, bearerMalformed
		}
		found = true
		value = values[0]
	}
	if !found {
		return Token{}, bearerMissing
	}
	if value == "" || !asciiHeaderValue(value) {
		return Token{}, bearerMalformed
	}

	separator := strings.IndexByte(value, ' ')
	if separator < 0 {
		if strings.EqualFold(value, "Bearer") {
			return Token{}, bearerMalformed
		}
		return Token{}, bearerUnsupported
	}
	if separator == 0 {
		return Token{}, bearerMalformed
	}
	if !strings.EqualFold(value[:separator], "Bearer") {
		return Token{}, bearerUnsupported
	}
	if strings.Contains(value, ",") {
		return Token{}, bearerMalformed
	}
	offset := separator
	for offset < len(value) && value[offset] == ' ' {
		offset++
	}
	if offset == len(value) {
		return Token{}, bearerMalformed
	}
	encoded := value[offset:]
	if len(encoded) > MaxTokenBytes || !validB64Token(encoded) {
		return Token{}, bearerMalformed
	}
	return newToken(encoded), bearerAccepted
}

func asciiHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func validB64Token(value string) bool {
	if value == "" {
		return false
	}
	content := 0
	padding := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '=' {
			padding = true
			continue
		}
		if padding || !b64TokenCharacter(character) {
			return false
		}
		content++
	}
	return content > 0
}

func b64TokenCharacter(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' ||
		strings.ContainsRune("-._~+/", rune(character))
}

func preservedContextError(ctx context.Context, err error) error {
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}

func denialResponse(status int, code api.ResponseCode, challenge string) (web.Response, error) {
	response, err := api.ErrorResponse(status, code, validation.NewErrors())
	if err != nil {
		return web.Response{}, &Error{Code: CodeResponse, Field: "body", Detail: "Bearer denial response could not be created", Cause: err}
	}
	header := response.Header()
	header.Set("WWW-Authenticate", challenge)
	response, err = web.NewResponse(response.Status(), header, response.Body())
	if err != nil {
		return web.Response{}, &Error{Code: CodeResponse, Field: "challenge", Detail: "Bearer challenge could not be applied", Cause: err}
	}
	return response, nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// Keep fmt imported as a compile-time assertion that Config's redacted String
// surface remains usable without exposing either injected collaborator.
var _ fmt.Stringer = Config{}
