package sessionauth

import (
	"crypto/rand"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
)

const (
	DefaultSessionCookieName = "godj_session"
	DefaultCSRFCookieName    = "godj_csrf"
	DefaultCSRFHeader        = "X-GoDj-CSRFToken"
	defaultLoginPath         = "/admin/login/"
	defaultFallbackPath      = "/admin/"
	defaultCSRFLifetime      = 24 * time.Hour
	maximumCookieLifetime    = 365 * 24 * time.Hour
	defaultMaxCookieHeader   = 16 * 1024
	defaultMaxCookieValue    = 256
	defaultMaxCSRFToken      = 256
	defaultMaxNext           = 2048
	// One Admin registry can publish 1 + 5*256 protected GET paths.
	defaultMaxAllowedPaths = 2048
	hardMaxCookieHeader    = 64 * 1024
	hardMaxCookieValue     = 4096
	hardMaxCSRFToken       = 4096
	hardMaxNext            = 8192
	hardMaxAllowedPaths    = 4096
)

type CookieConfig struct {
	Name          string
	Path          string
	Domain        string
	Secure        bool
	AllowInsecure bool
	SameSite      http.SameSite
	Lifetime      time.Duration
}

type Limits struct {
	MaxCookieHeaderBytes int
	MaxCookieValueBytes  int
	MaxCSRFTokenBytes    int
	MaxNextBytes         int
	MaxAllowedNextPaths  int
}

func DefaultLimits() Limits {
	return Limits{
		MaxCookieHeaderBytes: defaultMaxCookieHeader,
		MaxCookieValueBytes:  defaultMaxCookieValue,
		MaxCSRFTokenBytes:    defaultMaxCSRFToken,
		MaxNextBytes:         defaultMaxNext,
		MaxAllowedNextPaths:  defaultMaxAllowedPaths,
	}
}

type Config struct {
	Sessions         *sessions.Manager
	Authenticator    auth.CredentialAuthenticator
	Authorizer       auth.Authorizer
	SessionCookie    CookieConfig
	CSRFCookie       CookieConfig
	CSRFHeader       string
	LoginPath        string
	FallbackPath     string
	AllowedNextPaths []string
	Random           io.Reader
	Clock            func() time.Time
	Limits           Limits
}

type Runtime struct {
	sessions         *sessions.Manager
	authenticator    auth.CredentialAuthenticator
	authorizer       auth.Authorizer
	sessionCookie    CookieConfig
	csrfCookie       CookieConfig
	csrfHeader       string
	loginPath        string
	fallbackPath     string
	allowedNextPaths map[string]struct{}
	random           io.Reader
	entropyMu        sync.Mutex
	clock            func() time.Time
	clockMu          sync.Mutex
	limits           Limits
	csrfKey          [csrfSecretBytes]byte
}

func (*Runtime) String() string   { return "sessionauth.Runtime{redacted}" }
func (*Runtime) GoString() string { return "sessionauth.Runtime{redacted}" }

func (r *Runtime) LoginPath() string {
	if r == nil {
		return ""
	}
	return r.loginPath
}

func (r *Runtime) FallbackPath() string {
	if r == nil {
		return ""
	}
	return r.fallbackPath
}

// CSRFHeader returns the configured request-header name. API adapters may
// reuse this public name for a freshly masked response token without exposing
// the independent HttpOnly cookie secret.
func (r *Runtime) CSRFHeader() string {
	if r == nil {
		return ""
	}
	return r.csrfHeader
}

// AllowsNext reports whether raw is a canonical bounded request URI accepted
// by this runtime rather than merely returning the fallback chosen by SafeNext.
func (r *Runtime) AllowsNext(raw string) bool {
	return r != nil && validLocalRequestURI(raw, r.allowedNextPaths, r.limits.MaxNextBytes)
}

// AllowedNextPaths returns a detached, lexically sorted snapshot of the exact
// local paths accepted by SafeNext. Query strings are never stored here.
func (r *Runtime) AllowedNextPaths() []string {
	if r == nil {
		return nil
	}
	paths := make([]string, 0, len(r.allowedNextPaths))
	for candidate := range r.allowedNextPaths {
		paths = append(paths, candidate)
	}
	sort.Strings(paths)
	return paths
}

// CookiesApplyTo reports whether both bearer-cookie paths cover requestPath.
// It exposes no cookie value or secret and lets a composed HTTP surface reject
// a configuration whose login/logout paths cannot receive or delete a cookie.
func (r *Runtime) CookiesApplyTo(requestPath string) bool {
	return r != nil && validStaticPath(requestPath) &&
		cookiePathMatches(r.sessionCookie.Path, requestPath) &&
		cookiePathMatches(r.csrfCookie.Path, requestPath)
}

func New(config Config) (*Runtime, error) {
	if config.Sessions == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "sessions", Detail: "session manager is nil"}
	}
	if config.Authenticator == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "authenticator", Detail: "credential authenticator is nil"}
	}
	if config.Authorizer == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "authorizer", Detail: "authorizer is nil"}
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	limits, err := normalizeLimits(config.Limits)
	if err != nil {
		return nil, err
	}
	sessionCookie, err := normalizeCookie(config.SessionCookie, DefaultSessionCookieName, 0)
	if err != nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "session_cookie", Detail: "session cookie configuration is invalid", Cause: err}
	}
	csrfCookie, err := normalizeCookie(config.CSRFCookie, DefaultCSRFCookieName, defaultCSRFLifetime)
	if err != nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "csrf_cookie", Detail: "CSRF cookie configuration is invalid", Cause: err}
	}
	if sessionCookie.Name == csrfCookie.Name {
		return nil, &Error{Code: CodeInvalidConfig, Field: "cookies", Detail: "session and CSRF cookie names must differ"}
	}
	if csrfCookie.Domain != "" {
		return nil, &Error{Code: CodeInvalidConfig, Field: "csrf_cookie", Detail: "CSRF cookie must be host-only"}
	}
	if config.CSRFHeader == "" {
		config.CSRFHeader = DefaultCSRFHeader
	}
	if !validCSRFHeaderName(config.CSRFHeader) {
		return nil, &Error{Code: CodeInvalidConfig, Field: "csrf_header", Detail: "CSRF header name must be a non-proxy X- custom HTTP header"}
	}
	if config.LoginPath == "" {
		config.LoginPath = defaultLoginPath
	}
	if config.FallbackPath == "" {
		config.FallbackPath = defaultFallbackPath
	}
	if !validStaticPath(config.LoginPath) || !validStaticPath(config.FallbackPath) {
		return nil, &Error{Code: CodeInvalidConfig, Field: "redirect_paths", Detail: "login and fallback paths must be clean local absolute static paths"}
	}
	if len(config.AllowedNextPaths) == 0 {
		config.AllowedNextPaths = []string{config.FallbackPath}
	}
	if len(config.AllowedNextPaths) > limits.MaxAllowedNextPaths {
		return nil, &Error{Code: CodeInvalidConfig, Field: "allowed_next_paths", Detail: "allowed next path count exceeds the configured limit"}
	}
	allowed := make(map[string]struct{}, len(config.AllowedNextPaths)+1)
	for _, candidate := range config.AllowedNextPaths {
		if !validStaticPath(candidate) {
			return nil, &Error{Code: CodeInvalidConfig, Field: "allowed_next_paths", Detail: "allowed next path is invalid"}
		}
		if _, duplicate := allowed[candidate]; duplicate {
			return nil, &Error{Code: CodeInvalidConfig, Field: "allowed_next_paths", Detail: "allowed next path is duplicated"}
		}
		allowed[candidate] = struct{}{}
	}
	if _, exists := allowed[config.FallbackPath]; !exists && len(allowed) == limits.MaxAllowedNextPaths {
		return nil, &Error{Code: CodeInvalidConfig, Field: "allowed_next_paths", Detail: "fallback path exceeds the configured allowed-path limit"}
	}
	allowed[config.FallbackPath] = struct{}{}
	var csrfKey [csrfSecretBytes]byte
	if _, err := io.ReadFull(config.Random, csrfKey[:]); err != nil {
		return nil, &Error{Code: CodeEntropy, Detail: "CSRF runtime key source failed", Cause: err}
	}
	return &Runtime{
		sessions:         config.Sessions,
		authenticator:    config.Authenticator,
		authorizer:       config.Authorizer,
		sessionCookie:    sessionCookie,
		csrfCookie:       csrfCookie,
		csrfHeader:       http.CanonicalHeaderKey(config.CSRFHeader),
		loginPath:        config.LoginPath,
		fallbackPath:     config.FallbackPath,
		allowedNextPaths: allowed,
		random:           config.Random,
		clock:            config.Clock,
		limits:           limits,
		csrfKey:          csrfKey,
	}, nil
}

func normalizeLimits(limits Limits) (Limits, error) {
	defaults := DefaultLimits()
	if limits.MaxCookieHeaderBytes == 0 {
		limits.MaxCookieHeaderBytes = defaults.MaxCookieHeaderBytes
	}
	if limits.MaxCookieValueBytes == 0 {
		limits.MaxCookieValueBytes = defaults.MaxCookieValueBytes
	}
	if limits.MaxCSRFTokenBytes == 0 {
		limits.MaxCSRFTokenBytes = defaults.MaxCSRFTokenBytes
	}
	if limits.MaxNextBytes == 0 {
		limits.MaxNextBytes = defaults.MaxNextBytes
	}
	if limits.MaxAllowedNextPaths == 0 {
		limits.MaxAllowedNextPaths = defaults.MaxAllowedNextPaths
	}
	if limits.MaxCookieHeaderBytes < 1 || limits.MaxCookieHeaderBytes > hardMaxCookieHeader ||
		limits.MaxCookieValueBytes < 43 || limits.MaxCookieValueBytes > hardMaxCookieValue ||
		limits.MaxCSRFTokenBytes < 128 || limits.MaxCSRFTokenBytes > hardMaxCSRFToken ||
		limits.MaxNextBytes < 1 || limits.MaxNextBytes > hardMaxNext ||
		limits.MaxAllowedNextPaths < 1 || limits.MaxAllowedNextPaths > hardMaxAllowedPaths {
		return Limits{}, &Error{Code: CodeInvalidConfig, Field: "limits", Detail: "session-auth limits are outside the supported range"}
	}
	return limits, nil
}

func normalizeCookie(config CookieConfig, defaultName string, defaultLifetime time.Duration) (CookieConfig, error) {
	if config.Name == "" {
		config.Name = defaultName
	}
	if config.Path == "" {
		config.Path = "/"
	}
	if config.SameSite == 0 || config.SameSite == http.SameSiteDefaultMode {
		config.SameSite = http.SameSiteLaxMode
	}
	if config.Lifetime == 0 {
		config.Lifetime = defaultLifetime
	}
	if config.AllowInsecure {
		if config.Secure {
			return CookieConfig{}, &Error{Code: CodeInvalidConfig, Detail: "cookie Secure and AllowInsecure settings conflict"}
		}
	} else {
		config.Secure = true
	}
	if len(config.Name) > 64 || !validHTTPToken(config.Name) || len(config.Path) > 256 ||
		!validStaticPath(config.Path) || len(config.Domain) > 253 || strings.ContainsAny(config.Domain, "\r\n\x00;") ||
		(config.SameSite != http.SameSiteLaxMode && config.SameSite != http.SameSiteStrictMode) ||
		config.Lifetime < 0 || config.Lifetime > maximumCookieLifetime {
		return CookieConfig{}, &Error{Code: CodeInvalidConfig, Detail: "cookie attributes are outside the supported profile"}
	}
	probe := &http.Cookie{Name: config.Name, Value: "probe", Path: config.Path, Domain: config.Domain, Secure: config.Secure, HttpOnly: true, SameSite: config.SameSite}
	if err := probe.Valid(); err != nil {
		return CookieConfig{}, &Error{Code: CodeInvalidConfig, Detail: "cookie attributes are invalid", Cause: err}
	}
	return config, nil
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validCSRFHeaderName(value string) bool {
	if !validHTTPToken(value) {
		return false
	}
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "x-") &&
		!strings.HasPrefix(lower, "x-forwarded-") &&
		lower != "x-real-ip"
}

func validStaticPath(value string) bool {
	if value == "" || len(value) > hardMaxNext || !utf8.ValidString(value) || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.ContainsAny(value, "?#\\{}:<>*\r\n\x00") {
		return false
	}
	candidate := value
	if value != "/" && strings.HasSuffix(value, "/") {
		candidate = strings.TrimSuffix(value, "/")
	}
	return pathpkg.Clean(candidate) == candidate
}

func validLocalRequestURI(raw string, allowed map[string]struct{}, maxBytes int) bool {
	if raw == "" || len(raw) > maxBytes || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\r\n\x00") || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return false
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawPath != "" {
		return false
	}
	if _, ok := allowed[parsed.Path]; !ok || !validStaticPath(parsed.Path) {
		return false
	}
	if parsed.RawQuery != "" {
		if _, err := url.ParseQuery(parsed.RawQuery); err != nil {
			return false
		}
	}
	return parsed.RequestURI() == raw
}

func cookiePathMatches(cookiePath, requestPath string) bool {
	if cookiePath == requestPath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/'
}
