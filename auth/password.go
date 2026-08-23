package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
)

const (
	passwordAlgorithm       = "pbkdf2_sha256"
	passwordFormatVersion   = "v1"
	defaultIterations       = 600_000
	defaultSaltBytes        = 16
	defaultKeyBytes         = 32
	defaultMaxPasswordBytes = 1024
	defaultMaxEncodedBytes  = 1024
	minimumIterations       = 10_000
	maximumIterations       = 2_000_000
	minimumSaltBytes        = 16
	maximumSaltBytes        = 64
	minimumKeyBytes         = 32
	maximumKeyBytes         = 64
	hardMaxPasswordBytes    = 4096
	hardMaxEncodedBytes     = 2048
)

// PasswordHasher hashes and verifies password strings without exposing an
// encoded hash through an error. Implementations must be safe for concurrent
// use.
type PasswordHasher interface {
	Hash(context.Context, string) (string, error)
	Verify(context.Context, string, string) (bool, error)
	// ValidateEncoded must reject hashes whose work profile differs from the
	// hasher's current dummy-hash profile so unknown and known users consume the
	// same bounded credential work.
	ValidateEncoded(string) error
}

type PBKDF2Config struct {
	Iterations       int
	SaltBytes        int
	KeyBytes         int
	MaxPasswordBytes int
	MaxEncodedBytes  int
	Random           io.Reader
}

// PBKDF2 implements the current PBKDF2-HMAC-SHA256 encoded password profile.
type PBKDF2 struct {
	iterations       int
	saltBytes        int
	keyBytes         int
	maxPasswordBytes int
	maxEncodedBytes  int
	random           io.Reader
	entropyMu        sync.Mutex
}

func (*PBKDF2) String() string   { return "auth.PBKDF2{redacted}" }
func (*PBKDF2) GoString() string { return "auth.PBKDF2{redacted}" }

func NewDefaultPBKDF2() (*PBKDF2, error) { return NewPBKDF2(PBKDF2Config{}) }

func NewPBKDF2(config PBKDF2Config) (*PBKDF2, error) {
	if config.Iterations == 0 {
		config.Iterations = defaultIterations
	}
	if config.SaltBytes == 0 {
		config.SaltBytes = defaultSaltBytes
	}
	if config.KeyBytes == 0 {
		config.KeyBytes = defaultKeyBytes
	}
	if config.MaxPasswordBytes == 0 {
		config.MaxPasswordBytes = defaultMaxPasswordBytes
	}
	if config.MaxEncodedBytes == 0 {
		config.MaxEncodedBytes = defaultMaxEncodedBytes
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Iterations < minimumIterations || config.Iterations > maximumIterations ||
		config.SaltBytes < minimumSaltBytes || config.SaltBytes > maximumSaltBytes ||
		config.KeyBytes < minimumKeyBytes || config.KeyBytes > maximumKeyBytes ||
		config.MaxPasswordBytes < 1 || config.MaxPasswordBytes > hardMaxPasswordBytes ||
		config.MaxEncodedBytes < 128 || config.MaxEncodedBytes > hardMaxEncodedBytes {
		return nil, &Error{Code: CodeInvalidConfig, Field: "pbkdf2", Detail: "password hashing parameters are outside the supported range"}
	}
	return &PBKDF2{
		iterations:       config.Iterations,
		saltBytes:        config.SaltBytes,
		keyBytes:         config.KeyBytes,
		maxPasswordBytes: config.MaxPasswordBytes,
		maxEncodedBytes:  config.MaxEncodedBytes,
		random:           config.Random,
	}, nil
}

func (h *PBKDF2) Hash(ctx context.Context, password string) (string, error) {
	if err := h.validCall(ctx, password); err != nil {
		return "", err
	}
	salt := make([]byte, h.saltBytes)
	h.entropyMu.Lock()
	if _, err := io.ReadFull(h.random, salt); err != nil {
		h.entropyMu.Unlock()
		return "", &Error{Code: CodeEntropy, Detail: "password salt source failed", Cause: err}
	}
	h.entropyMu.Unlock()
	key, err := pbkdf2.Key(sha256.New, password, salt, h.iterations, h.keyBytes)
	if err != nil {
		return "", &Error{Code: CodeCredential, Detail: "password derivation failed", Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	encoded := strings.Join([]string{
		passwordAlgorithm,
		passwordFormatVersion,
		strconv.Itoa(h.iterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	}, "$")
	if len(encoded) > h.maxEncodedBytes {
		return "", &Error{Code: CodeInvalidConfig, Field: "encoded_password", Detail: "configured password hash exceeds the encoded resource limit"}
	}
	return encoded, nil
}

func (h *PBKDF2) Verify(ctx context.Context, password, encoded string) (bool, error) {
	if err := h.validCall(ctx, password); err != nil {
		return false, err
	}
	parsed, err := h.parse(encoded)
	if err != nil {
		return false, err
	}
	derived, err := pbkdf2.Key(sha256.New, password, parsed.salt, parsed.iterations, len(parsed.key))
	if err != nil {
		return false, &Error{Code: CodeCredential, Detail: "password derivation failed", Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(derived, parsed.key) == 1, nil
}

// ValidateEncoded checks one stored hash without deriving a password key.
func (h *PBKDF2) ValidateEncoded(encoded string) error {
	if h == nil {
		return &Error{Code: CodeInvalidConfig, Detail: "password hasher is nil or uninitialized"}
	}
	parsed, err := h.parse(encoded)
	if err != nil {
		return err
	}
	if parsed.iterations != h.iterations || len(parsed.salt) != h.saltBytes || len(parsed.key) != h.keyBytes {
		return &Error{Code: CodeInvalidHash, Field: "encoded_password", Detail: "encoded password does not use the current bounded work profile"}
	}
	return nil
}

type parsedPassword struct {
	iterations int
	salt       []byte
	key        []byte
}

func (h *PBKDF2) parse(encoded string) (parsedPassword, error) {
	invalid := func() (parsedPassword, error) {
		return parsedPassword{}, &Error{Code: CodeInvalidHash, Field: "encoded_password", Detail: "encoded password is malformed or outside resource limits"}
	}
	if encoded == "" || len(encoded) > h.maxEncodedBytes || strings.ContainsAny(encoded, "\r\n\x00") {
		return invalid()
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != passwordAlgorithm || parts[1] != passwordFormatVersion {
		return invalid()
	}
	iterations, err := strconv.Atoi(parts[2])
	if err != nil || strconv.Itoa(iterations) != parts[2] || iterations < minimumIterations || iterations > maximumIterations {
		return invalid()
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[3])
	if err != nil || len(salt) < minimumSaltBytes || len(salt) > maximumSaltBytes || base64.RawStdEncoding.EncodeToString(salt) != parts[3] {
		return invalid()
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(key) < minimumKeyBytes || len(key) > maximumKeyBytes || base64.RawStdEncoding.EncodeToString(key) != parts[4] {
		return invalid()
	}
	return parsedPassword{iterations: iterations, salt: salt, key: key}, nil
}

func (h *PBKDF2) validCall(ctx context.Context, password string) error {
	if ctx == nil {
		return &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if h == nil || h.random == nil || h.iterations == 0 {
		return &Error{Code: CodeInvalidConfig, Detail: "password hasher is nil or uninitialized"}
	}
	if len(password) > h.maxPasswordBytes {
		return &Error{Code: CodeInvalidInput, Field: "password", Detail: "password exceeds the supported byte limit"}
	}
	return nil
}

func passwordFailure(cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &Error{Code: CodeCredential, Detail: "credential verification failed", Cause: cause}
}
