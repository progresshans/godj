package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/emailinput"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/mail"
	"github.com/progresshans/godj/validation"
)

// MaximumPasswordResetCandidates bounds the complete active, database-iexact
// candidate set before usable-password and Unicode comparison. Overflow is an
// explicit error; a truncated prefix must never receive reset messages.
const MaximumPasswordResetCandidates = 256

// MaximumPasswordResetMailBytes bounds combined subject/plain/HTML content per
// recipient. Preparing the complete batch therefore retains at most 16 MiB.
const MaximumPasswordResetMailBytes = 64 * 1024

// PasswordResetMail contains immutable recipient-only template data. It carries
// no password hash, credential stamp or mutable account object. Formatting and
// JSON deliberately hide the link and account information.
type PasswordResetMail struct{ state *passwordResetMail }
type passwordResetMail struct{ username, siteName, link string }

func (PasswordResetMail) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordResetMail{redacted}"))
}
func (PasswordResetMail) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.PasswordResetMail{redacted}"`), nil
}
func (value PasswordResetMail) Username() string {
	if value.state == nil {
		return ""
	}
	return value.state.username
}
func (value PasswordResetMail) SiteName() string {
	if value.state == nil {
		return ""
	}
	return value.state.siteName
}
func (value PasswordResetMail) Link() string {
	if value.state == nil {
		return ""
	}
	return value.state.link
}

type PasswordResetMailContent struct{ Subject, Text, HTML string }

func (PasswordResetMailContent) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordResetMailContent{redacted}"))
}
func (PasswordResetMailContent) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.PasswordResetMailContent{redacted}"`), nil
}

// PasswordResetMailRenderer must be concurrency-safe and respect context. It
// owns content only: From and the snapshot-bound recipient remain framework
// owned. Render is called after all database scopes are closed, before any mail
// is sent. A rendering failure therefore cannot publish a partial prefix.
type PasswordResetMailRenderer interface {
	RenderPasswordResetMail(context.Context, PasswordResetMail) (PasswordResetMailContent, error)
}
type PasswordResetMailRendererFunc func(context.Context, PasswordResetMail) (PasswordResetMailContent, error)

func (fn PasswordResetMailRendererFunc) RenderPasswordResetMail(ctx context.Context, data PasswordResetMail) (PasswordResetMailContent, error) {
	return fn(ctx, data)
}

type PasswordResetMailConfig struct {
	From mail.Address
	// Origin is an explicit https origin, never derived from an incoming Host
	// or forwarding header. HTTP is allowed only on a literal loopback IP.
	Origin string
	// ConfirmPath is a canonical path without a trailing slash, query or route
	// placeholders. Links append /<base64url principal ID>/<token>/.
	ConfirmPath string
	SiteName    string
	// Nil uses a plain-text message with the site name and reset link. A custom
	// renderer can use the host's template engine and add HTML content.
	Renderer PasswordResetMailRenderer `json:"-"`
}

func (PasswordResetMailConfig) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordResetMailConfig{redacted}"))
}
func (PasswordResetMailConfig) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.PasswordResetMailConfig{redacted}"`), nil
}

// PasswordResetMailer selects and delivers reset links. It never hashes or
// changes a user, session, audit or login timestamp. The host must give valid
// email submissions the same public acknowledgement, including delivery or
// infrastructure failures, and report the returned private error separately.
// Synchronous I/O is not a constant-time account-existence guarantee.
type PasswordResetMailer struct{ state *passwordResetMailer }
type passwordResetMailer struct {
	resetter *PasswordResetter
	sender   mail.Sender
	config   PasswordResetMailConfig
}

func (*PasswordResetMailer) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordResetMailer{redacted}"))
}
func (*PasswordResetMailer) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.PasswordResetMailer{redacted}"`), nil
}

func NewPasswordResetMailer(resetter *PasswordResetter, sender mail.Sender, config PasswordResetMailConfig) (*PasswordResetMailer, error) {
	if resetter == nil || resetter.state == nil || nilIdentityValue(sender) || config.From.Mailbox() == "" {
		return nil, managementError(CodeInvalidConfig, "password_reset_mail", nil)
	}
	origin, err := url.Parse(config.Origin)
	if err != nil || origin == nil || origin.Host == "" || origin.Hostname() == "" || strings.HasSuffix(origin.Host, ":") || origin.Opaque != "" || origin.User != nil || origin.Path != "" ||
		origin.RawPath != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.RawFragment != "" ||
		strings.ContainsAny(config.Origin, "\\\r\n\t #?") || len(config.Origin) > 2048 {
		return nil, managementError(CodeInvalidConfig, "password_reset_origin", nil)
	}
	if origin.Scheme != "https" {
		ip := net.ParseIP(origin.Hostname())
		if origin.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return nil, managementError(CodeInvalidConfig, "password_reset_origin", nil)
		}
	}
	if port := origin.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 || strconv.Itoa(value) != port {
			return nil, managementError(CodeInvalidConfig, "password_reset_origin", nil)
		}
	}
	if config.ConfirmPath == "" {
		config.ConfirmPath = "/account/reset"
	}
	if len(config.ConfirmPath) > 128 || !utf8.ValidString(config.ConfirmPath) || config.ConfirmPath == "/" ||
		!strings.HasPrefix(config.ConfirmPath, "/") || path.Clean(config.ConfirmPath) != config.ConfirmPath ||
		strings.ContainsAny(config.ConfirmPath, "\\%?#<> ") || strings.IndexFunc(config.ConfirmPath, func(char rune) bool { return char < 32 || char == 127 }) >= 0 {
		return nil, managementError(CodeInvalidConfig, "password_reset_path", nil)
	}
	if config.SiteName == "" {
		config.SiteName = "GoDj"
	}
	if !utf8.ValidString(config.SiteName) || len(config.SiteName) > 256 || strings.IndexFunc(config.SiteName, func(char rune) bool { return char < 32 || char == 127 }) >= 0 {
		return nil, managementError(CodeInvalidConfig, "password_reset_site", nil)
	}
	if config.Renderer == nil {
		config.Renderer = PasswordResetMailRendererFunc(defaultPasswordResetMail)
	} else if nilIdentityValue(config.Renderer) {
		return nil, managementError(CodeInvalidConfig, "password_reset_renderer", nil)
	}
	config.Origin = origin.String()
	return &PasswordResetMailer{&passwordResetMailer{resetter, sender, config}}, nil
}

func defaultPasswordResetMail(_ context.Context, data PasswordResetMail) (PasswordResetMailContent, error) {
	return PasswordResetMailContent{
		Subject: "Password reset on " + data.SiteName(),
		Text:    "You requested a password reset on " + data.SiteName() + ".\n\nOpen this link to choose a new password:\n" + data.Link() + "\n\nIf you did not request this, you can ignore this message.\n",
	}, nil
}

// CleanPasswordResetEmail shares native PasswordResetForm's Unicode stripping,
// required, grammar, character-length and NUL diagnostics with its consumers.
// It returns no cleaned value when any diagnostic exists. UTF-8 and a 4096-byte
// raw input bound are Go transport limits, checked before Unicode work.
func CleanPasswordResetEmail(raw string) (string, validation.Errors) {
	if !utf8.ValidString(raw) || len(raw) > 4096 {
		return "", validation.NewErrors(validation.New("email", "invalid"))
	}
	value := unicode16.TrimSpace(raw)
	if value == "" {
		return "", validation.NewErrors(validation.New("email", "required"))
	}
	if failures := emailinput.FormErrors("email", value, 254); !failures.Empty() {
		return "", failures
	}
	return value, validation.Errors{}
}

// ConfirmPath reports the normalized recipient link prefix without I/O or
// credential material. An uninitialized mailer returns an empty path.
func (mailer *PasswordResetMailer) ConfirmPath() string {
	if mailer == nil || mailer.state == nil {
		return ""
	}
	return mailer.state.config.ConfirmPath
}

// Request returns nil for no eligible users as well as confirmed deliveries.
// All candidates and token-bound recipient data come from one complete read
// snapshot. It closes before rendering and mail I/O. No later token issuance
// may pair a changed account state with a previously selected email address.
// Every selected message is attempted once, continuing after an ordinary send
// failure. Errors preserve those failures (including unknown outcomes) without
// exposing addresses or links, and the operation never automatically retries.
func (mailer *PasswordResetMailer) Request(ctx context.Context, email string) error {
	if ctx == nil {
		return managementError(CodeInvalidInput, "context", nil)
	}
	if err := ctx.Err(); err != nil {
		return managementError(CodePersistence, "password_reset_request", err)
	}
	if mailer == nil || mailer.state == nil {
		return managementError(CodeInvalidConfig, "password_reset_mail", nil)
	}
	cleaned, failures := CleanPasswordResetEmail(email)
	if !failures.Empty() {
		return validation.Reject(failures, nil)
	}
	state := mailer.state
	accounts, err := state.candidates(ctx, cleaned)
	if err != nil {
		return err
	}
	now, err := state.resetter.instant()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return managementError(CodePersistence, "password_reset_request", err)
	}
	messages := make([]mail.Message, 0, len(accounts))
	for _, account := range accounts {
		profile := account.Profile()
		recipient, err := mail.ParseAddress(profile.Email)
		if err != nil {
			return managementError(CodeDelivery, "password_reset_recipient", err)
		}
		token := state.resetter.state.keys.issue(account, now)
		link := state.config.Origin + state.config.ConfirmPath + "/" + base64.RawURLEncoding.EncodeToString([]byte(profile.PrincipalID)) + "/" + token.Encoded() + "/"
		data := PasswordResetMail{&passwordResetMail{profile.Username, state.config.SiteName, link}}
		content, err := state.config.Renderer.RenderPasswordResetMail(ctx, data)
		if err = errors.Join(err, ctx.Err()); err != nil {
			return managementError(CodeDelivery, "password_reset_render", err)
		}
		remaining := MaximumPasswordResetMailBytes
		for _, value := range []string{content.Subject, content.Text, content.HTML} {
			if len(value) > remaining {
				return managementError(CodeDelivery, "password_reset_render_size", nil)
			}
			remaining -= len(value)
		}
		message, err := mail.NewMessage(mail.MessageConfig{From: state.config.From, To: []mail.Address{recipient}, Subject: content.Subject, Text: content.Text, HTML: content.HTML})
		if err != nil {
			return managementError(CodeDelivery, "password_reset_render", err)
		}
		messages = append(messages, message)
	}
	var deliveryErrors []error
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			deliveryErrors = append(deliveryErrors, err)
			break
		}
		if err := state.sender.Send(ctx, message); err != nil {
			deliveryErrors = append(deliveryErrors, err)
		}
	}
	if err := errors.Join(deliveryErrors...); err != nil {
		code := CodeDelivery
		if errors.Is(err, &mail.Error{Code: mail.CodeOutcomeUnknown}) {
			code = CodeOutcomeUnknown
		}
		return managementError(code, "password_reset_delivery", err)
	}
	// A sender's confirmed acceptance is not revoked by late cancellation.
	return nil
}

func (state *passwordResetMailer) candidates(ctx context.Context, email string) ([]Account, error) {
	var accounts []Account
	var callbackErr error
	calls := 0
	err := state.resetter.state.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || nilIdentityValue(reader) {
			callbackErr = managementError(CodePersistence, "snapshot_contract", nil)
			return callbackErr
		}
		callbackErr = func() error {
			query, err := models.UserObjects.Using(reader).Filter(models.UserFields.Active.Exact(true), models.UserFields.Email.IExact(email)).OrderBy(models.UserFields.ID.Asc()).Limit(MaximumPasswordResetCandidates + 1)
			if err != nil {
				return err
			}
			rows, err := query.All(ctx)
			if err != nil {
				return err
			}
			if len(rows) > MaximumPasswordResetCandidates {
				return managementError(CodePersistence, "password_reset_candidates", nil)
			}
			folded := unicode16.CaseFold(unicode16.NFKC(email))
			for _, row := range rows {
				if unicode16.CaseFold(unicode16.NFKC(row.Email)) != folded {
					continue
				}
				account, err := state.resetter.state.directory.accountFromRow(ctx, reader, row)
				if err != nil {
					return err
				}
				if account.value().credential.Principal().Authenticated() && account.HasUsablePassword() {
					accounts = append(accounts, account)
				}
			}
			return nil
		}()
		return callbackErr
	})
	if err = errors.Join(err, callbackErr, ctx.Err()); err != nil {
		return nil, managementError(CodePersistence, "password_reset_candidates", err)
	}
	if calls != 1 {
		return nil, managementError(CodePersistence, "snapshot_contract", nil)
	}
	return accounts, nil
}
