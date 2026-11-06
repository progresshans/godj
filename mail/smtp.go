package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type SMTPSecurity string

const (
	SMTPStartTLS    SMTPSecurity = "starttls"
	SMTPImplicitTLS SMTPSecurity = "tls"
	// SMTPPlaintext must be selected explicitly and cannot carry authentication.
	SMTPPlaintext SMTPSecurity = "plaintext"
)

type SMTPConfig struct {
	// Address is a host:port, never a URL. Security has no implicit fallback.
	Address  string
	Security SMTPSecurity
	// ServerName defaults to the address host. RootCAs is cloned at construction;
	// nil uses system roots. Certificate validation is always enabled.
	ServerName string
	RootCAs    *x509.CertPool `json:"-"`
	// Only AUTH PLAIN over verified TLS is currently supported. Unsupported
	// mechanisms are explicit errors, never anonymous delivery fallbacks.
	Username string `json:"-"`
	Password string `json:"-"`
	// Timeout bounds the entire connection and SMTP exchange; zero selects 30s.
	Timeout time.Duration
	// LocalName is the EHLO identity. Empty selects localhost.
	LocalName string
}

func (SMTPConfig) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("mail.SMTPConfig{redacted}"))
}
func (SMTPConfig) MarshalJSON() ([]byte, error) { return []byte(`"mail.SMTPConfig{redacted}"`), nil }

// SMTP opens a separate, bounded connection for each message. It never retries
// or pools transaction state. All recipients must be accepted before DATA.
// Nil is a confirmed final DATA acknowledgement; subsequent cleanup failure
// cannot turn that acceptance into an error that invites duplicate delivery.
type SMTP struct{ state *smtpState }
type smtpState struct{ config SMTPConfig }

func (*SMTP) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("mail.SMTP{redacted}"))
}
func (*SMTP) MarshalJSON() ([]byte, error) { return []byte(`"mail.SMTP{redacted}"`), nil }

func NewSMTP(config SMTPConfig) (*SMTP, error) {
	host, port, err := net.SplitHostPort(config.Address)
	if err != nil {
		return nil, failure(CodeInvalidConfig, "smtp_address", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 || strconv.Itoa(portNumber) != port {
		return nil, failure(CodeInvalidConfig, "smtp_address", err)
	}
	host, err = smtpHost(host)
	if err != nil {
		return nil, err
	}
	config.Address = net.JoinHostPort(host, port)
	if config.ServerName == "" {
		config.ServerName = host
	}
	config.ServerName, err = smtpHost(config.ServerName)
	if err != nil {
		return nil, err
	}
	switch config.Security {
	case SMTPPlaintext:
		if config.Username != "" || config.Password != "" || config.RootCAs != nil || config.ServerName != host {
			return nil, failure(CodeInvalidConfig, "plaintext_credentials", nil)
		}
	case SMTPStartTLS, SMTPImplicitTLS:
	default:
		return nil, failure(CodeInvalidConfig, "smtp_security", nil)
	}
	if config.Username == "" && config.Password != "" || len(config.Username) > 1024 || len(config.Password) > 4096 ||
		strings.ContainsRune(config.Username, 0) || strings.ContainsRune(config.Password, 0) {
		return nil, failure(CodeInvalidConfig, "smtp_auth", nil)
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Timeout < 0 {
		return nil, failure(CodeInvalidConfig, "smtp_timeout", nil)
	}
	if config.LocalName == "" {
		config.LocalName = "localhost"
	}
	config.LocalName, err = smtpHost(config.LocalName)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(config.LocalName); ip != nil {
		// EHLO uses an address literal, not a bare numeric host.
		if ip.To4() == nil {
			config.LocalName = "[IPv6:" + config.LocalName + "]"
		} else {
			config.LocalName = "[" + config.LocalName + "]"
		}
	}
	if config.RootCAs != nil {
		config.RootCAs = config.RootCAs.Clone()
	}
	return &SMTP{&smtpState{config}}, nil
}

func smtpHost(value string) (string, error) {
	if value == "" || len(value) > 253 || !headerText(value) || strings.ContainsAny(value, " /\\@[]%") {
		return "", failure(CodeInvalidConfig, "smtp_host", nil)
	}
	if net.ParseIP(value) != nil {
		return value, nil
	}
	value, err := domainProfile.ToASCII(value)
	if err != nil || value == "" || strings.ContainsRune(value, ':') {
		return "", failure(CodeInvalidConfig, "smtp_host", err)
	}
	return value, nil
}

// Bound even an untrusted server's unterminated reply line. The limit includes
// TLS records when encryption is enabled and is ample for 256 recipient replies.
// The connection is abandoned on exhaustion, never reused with truncated input.
const maximumSMTPReplyBytes = 4 << 20

type smtpConn struct {
	net.Conn
	remaining int
}

func (connection *smtpConn) Read(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	if connection.remaining == 0 {
		return 0, errors.New("mail: SMTP reply exceeds limit")
	}
	n, err := connection.Conn.Read(value[:min(len(value), connection.remaining)])
	connection.remaining -= n
	return n, err
}

func (sender *SMTP) Send(ctx context.Context, message Message) error {
	if sender == nil || sender.state == nil {
		return failure(CodeInvalidConfig, "smtp", nil)
	}
	delivery, err := prepare(ctx, message)
	if err != nil {
		return err
	}
	config := sender.state.config
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	dialer := net.Dialer{Timeout: config.Timeout}
	raw, err := dialer.DialContext(ctx, "tcp", config.Address)
	if err != nil {
		return smtpFailure(ctx, "connect", err, false)
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := raw.SetDeadline(deadline); err != nil {
		return smtpFailure(ctx, "deadline", err, false)
	}
	var connection net.Conn = &smtpConn{raw, maximumSMTPReplyBytes}
	tlsConfig := &tls.Config{ServerName: config.ServerName, RootCAs: config.RootCAs, MinVersion: tls.VersionTLS12}
	if config.Security == SMTPImplicitTLS {
		secured := tls.Client(connection, tlsConfig)
		if err := secured.HandshakeContext(ctx); err != nil {
			return smtpFailure(ctx, "tls", err, false)
		}
		connection = secured
	}
	client, err := smtp.NewClient(connection, config.ServerName)
	if err != nil {
		return smtpFailure(ctx, "greeting", err, false)
	}
	defer client.Close()
	if err := client.Hello(config.LocalName); err != nil {
		return smtpFailure(ctx, "hello", err, false)
	}
	if config.Security == SMTPStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return failure(CodeUnsupported, "starttls", nil)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return smtpFailure(ctx, "starttls", err, false)
		}
	}
	if config.Username != "" {
		_, methods := client.Extension("AUTH")
		plain := false
		for _, method := range strings.Fields(methods) {
			plain = plain || strings.EqualFold(method, "PLAIN")
		}
		if !plain {
			return failure(CodeUnsupported, "auth", nil)
		}
		if err := client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.ServerName)); err != nil {
			return smtpFailure(ctx, "auth", err, false)
		}
	}
	if message.state.utf8 {
		if ok, _ := client.Extension("SMTPUTF8"); !ok {
			return failure(CodeUnsupported, "smtputf8", nil)
		}
	}
	if err := client.Mail(message.From().Mailbox()); err != nil {
		return smtpFailure(ctx, "mail", err, false)
	}
	for _, address := range message.Recipients() {
		if err := client.Rcpt(address.Mailbox()); err != nil {
			return smtpFailure(ctx, "recipient", err, false)
		}
	}
	// Use the public text protocol for DATA so the terminator's flush error
	// is checked as well as the reply. net/smtp's DATA Close discards that
	// write error before reading the response.
	response, err := client.Text.Cmd("DATA")
	if err != nil {
		return smtpFailure(ctx, "data", err, false)
	}
	client.Text.StartResponse(response)
	_, _, err = client.Text.ReadResponse(354)
	client.Text.EndResponse(response)
	if err != nil {
		return smtpFailure(ctx, "data", err, false)
	}
	writer := client.Text.DotWriter()
	if _, err := writer.Write(delivery.state.data); err != nil {
		// Do not close the DATA writer after a write failure: its Close emits
		// the final terminator and could submit a truncated message.
		return smtpFailure(ctx, "data_write", err, false)
	}
	if err := writer.Close(); err != nil {
		return smtpFailure(ctx, "data_acknowledgement", err, true)
	}
	if _, _, err := client.Text.ReadResponse(250); err != nil {
		return smtpFailure(ctx, "data_acknowledgement", err, true)
	}
	// RFC 5321 section 4.2.5 transfers responsibility at the positive final
	// DATA reply. QUIT, connection cleanup and late cancellation cannot revoke
	// that confirmation. They must not be returned as retryable send failures.
	_ = client.Quit()
	return nil
}

func smtpFailure(ctx context.Context, stage string, cause error, mayHaveAccepted bool) error {
	code := CodeNotSent
	if mayHaveAccepted {
		code = CodeOutcomeUnknown
	}
	var reply *textproto.Error
	if errors.As(cause, &reply) && reply.Code >= 400 && reply.Code <= 599 {
		code = CodeRejected
	}
	return failure(code, stage, errors.Join(cause, ctx.Err()))
}
