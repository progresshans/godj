package sessionauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"net/http"

	"github.com/progresshans/godj/web"
)

const (
	csrfSecretBytes       = 32
	csrfEncodedSecretSize = 43
	csrfMaskedBytes       = 96
	csrfEncodedTokenSize  = 128
)

type CSRFToken struct {
	value  string
	change ResponseChange
}

func (token CSRFToken) Value() string { return token.value }
func (token CSRFToken) Apply(response web.Response) (web.Response, error) {
	return token.change.Apply(response)
}
func (CSRFToken) String() string   { return "sessionauth.CSRFToken{redacted}" }
func (CSRFToken) GoString() string { return "sessionauth.CSRFToken{redacted}" }

// CSRFToken returns a freshly masked token. If the independent CSRF cookie is
// absent or malformed it also returns an immutable cookie change; it never
// creates or mutates a server-side session.
func (r *Runtime) CSRFToken(request *web.Request) (CSRFToken, error) {
	httpRequest, err := r.request(request)
	if err != nil {
		return CSRFToken{}, err
	}
	encoded, found, cookieErr := r.namedCookie(httpRequest, r.csrfCookie.Name)
	var secret []byte
	change := ResponseChange{}
	if cookieErr == nil && found {
		secret, err = decodeCSRFSecret(encoded)
	}
	if !found || cookieErr != nil || err != nil {
		encoded, err = r.newCSRFSecret()
		if err != nil {
			return CSRFToken{}, err
		}
		secret, _ = decodeCSRFSecret(encoded)
		change = ResponseChange{cookies: []http.Cookie{r.csrfResponseCookie(encoded)}}
	}
	mask := make([]byte, csrfSecretBytes)
	if err := r.readRandom(mask); err != nil {
		return CSRFToken{}, err
	}
	masked := make([]byte, csrfMaskedBytes)
	copy(masked, mask)
	for index := 0; index < csrfSecretBytes; index++ {
		masked[csrfSecretBytes+index] = mask[index] ^ secret[index]
	}
	// Bind the randomized prefix rather than the raw cookie secret so every
	// rendered token remains fully masked while sibling-domain cookie injection
	// still cannot manufacture a server-authenticated token.
	copy(masked[2*csrfSecretBytes:], r.csrfMAC(masked[:2*csrfSecretBytes]))
	return CSRFToken{value: base64.RawURLEncoding.EncodeToString(masked), change: change}, nil
}

// VerifyCSRF exempts HTTP safe methods and otherwise requires one canonical
// cookie secret plus one masked form or supported-header token. formTokens is
// a slice so duplicate form keys can be rejected before any mutation. When
// both token sources are present they must be identical.
func (r *Runtime) VerifyCSRF(request *web.Request, formTokens []string) error {
	httpRequest, err := r.request(request)
	if err != nil {
		return err
	}
	if safeMethod(httpRequest.Method) {
		return nil
	}
	encodedSecret, found, err := r.namedCookie(httpRequest, r.csrfCookie.Name)
	if err != nil || !found {
		return csrfRejected()
	}
	secret, err := decodeCSRFSecret(encodedSecret)
	if err != nil {
		return csrfRejected()
	}
	headerValues := httpRequest.Header.Values(r.csrfHeader)
	if len(headerValues) > 1 || len(formTokens) > 1 {
		return csrfRejected()
	}
	formToken := ""
	formPresent := len(formTokens) == 1
	headerPresent := len(headerValues) == 1
	if formPresent {
		formToken = formTokens[0]
	}
	candidate := formToken
	if formPresent && headerPresent && formToken != headerValues[0] {
		return csrfRejected()
	}
	if headerPresent {
		candidate = headerValues[0]
	}
	if len(candidate) != csrfEncodedTokenSize || len(candidate) > r.limits.MaxCSRFTokenBytes {
		return csrfRejected()
	}
	masked, err := base64.RawURLEncoding.Strict().DecodeString(candidate)
	if err != nil || len(masked) != csrfMaskedBytes || base64.RawURLEncoding.EncodeToString(masked) != candidate {
		return csrfRejected()
	}
	unmasked := make([]byte, csrfSecretBytes)
	for index := 0; index < csrfSecretBytes; index++ {
		unmasked[index] = masked[index] ^ masked[csrfSecretBytes+index]
	}
	secretMatches := subtle.ConstantTimeCompare(unmasked, secret)
	macMatches := subtle.ConstantTimeCompare(
		masked[2*csrfSecretBytes:],
		r.csrfMAC(masked[:2*csrfSecretBytes]),
	)
	if secretMatches&macMatches != 1 {
		return csrfRejected()
	}
	return nil
}

func (r *Runtime) newCSRFSecret() (string, error) {
	secret := make([]byte, csrfSecretBytes)
	if err := r.readRandom(secret); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}

func (r *Runtime) readRandom(target []byte) error {
	r.entropyMu.Lock()
	_, err := io.ReadFull(r.random, target)
	r.entropyMu.Unlock()
	if err != nil {
		return &Error{Code: CodeEntropy, Detail: "CSRF entropy source failed", Cause: err}
	}
	return nil
}

func (r *Runtime) csrfMAC(secret []byte) []byte {
	mac := hmac.New(sha256.New, r.csrfKey[:])
	_, _ = mac.Write(secret)
	return mac.Sum(nil)
}

func decodeCSRFSecret(encoded string) ([]byte, error) {
	if len(encoded) != csrfEncodedSecretSize {
		return nil, csrfRejected()
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(secret) != csrfSecretBytes || base64.RawURLEncoding.EncodeToString(secret) != encoded {
		return nil, csrfRejected()
	}
	return secret, nil
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func csrfRejected() error {
	return &Error{Code: CodeCSRFRejected, Detail: "CSRF verification failed"}
}
