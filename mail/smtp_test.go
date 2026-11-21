package mail_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/mail"
)

type smtpProbeConfig struct {
	security      mail.SMTPSecurity
	noStartTLS    bool
	noAuth        bool
	smtpUTF8      bool
	reject        string
	final         string
	quit          string
	stallGreeting bool
	hugeGreeting  bool
	dropBody      bool
	onConnect     func()
	onData        func()
	onQuit        func()
}

type smtpObservation struct {
	tls, authenticated, authAttempt bool
	hello                           string
	mail, recipients                []string
	data                            []byte
	dataCommands                    int
	err                             error
}

type smtpProbe struct {
	address string
	root    *x509.Certificate
	results <-chan smtpObservation
}

func startSMTPProbe(t *testing.T, config smtpProbeConfig) smtpProbe {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mail.test"}, DNSNames: []string{"mail.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, public, private)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan smtpObservation, 1)
	done := make(chan struct{})
	var mu sync.Mutex
	var active net.Conn
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		if active != nil {
			_ = active.Close()
		}
		mu.Unlock()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("SMTP probe did not terminate")
		}
	})
	go func() {
		defer close(done)
		defer listener.Close()
		var observed smtpObservation
		defer func() { results <- observed }()
		raw, err := listener.Accept()
		if err != nil {
			observed.err = err
			return
		}
		mu.Lock()
		active = raw
		mu.Unlock()
		defer raw.Close()
		if err := raw.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
			observed.err = err
			return
		}
		if config.onConnect != nil {
			config.onConnect()
		}
		var connection net.Conn = raw
		secure := func() error {
			tlsConnection := tls.Server(connection, tlsConfig)
			if err := tlsConnection.Handshake(); err != nil {
				return err
			}
			connection, observed.tls = tlsConnection, true
			return nil
		}
		if config.security == mail.SMTPImplicitTLS {
			if err := secure(); err != nil {
				observed.err = err
				return
			}
		}
		write := func(value string) bool {
			_, err := io.WriteString(connection, value+"\r\n")
			if err != nil {
				observed.err = err
			}
			return err == nil
		}
		if config.stallGreeting {
			_, observed.err = io.Copy(io.Discard, connection)
			return
		}
		if config.hugeGreeting {
			_, observed.err = io.WriteString(connection, "220 "+strings.Repeat("x", 5<<20))
			return
		}
		if !write("220 mail.test ESMTP probe") {
			return
		}
		reader := textproto.NewReader(bufio.NewReader(connection))
		for {
			line, err := reader.ReadLine()
			if err != nil {
				observed.err = err
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO "):
				observed.hello = strings.TrimPrefix(line, "EHLO ")
				capabilities := []string{"250-mail.test", "250-8BITMIME"}
				if config.security == mail.SMTPStartTLS && !observed.tls && !config.noStartTLS {
					capabilities = append(capabilities, "250-STARTTLS")
				}
				if !config.noAuth {
					capabilities = append(capabilities, "250-AUTH PLAIN")
				}
				if config.smtpUTF8 {
					capabilities = append(capabilities, "250-SMTPUTF8")
				}
				if !write(strings.Join(append(capabilities, "250 HELP"), "\r\n")) {
					return
				}
			case line == "STARTTLS":
				if observed.tls || config.noStartTLS || !write("220 ready for TLS") {
					return
				}
				if err := secure(); err != nil {
					observed.err = err
					return
				}
				reader = textproto.NewReader(bufio.NewReader(connection))
			case strings.HasPrefix(line, "AUTH "):
				observed.authAttempt = true
				expected := "AUTH PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00probe-user\x00probe-password"))
				observed.authenticated = observed.tls && line == expected && config.reject != "auth"
				if observed.authenticated {
					if !write("235 authenticated") {
						return
					}
				} else if !write("535 private-reply-authentication-failed") {
					return
				}
			case strings.HasPrefix(line, "MAIL FROM:"):
				observed.mail = append(observed.mail, line)
				if config.reject == "mail" {
					if !write("550 private-reply-sender-rejected") {
						return
					}
				} else if !write("250 sender accepted") {
					return
				}
			case strings.HasPrefix(line, "RCPT TO:"):
				observed.recipients = append(observed.recipients, strings.TrimSuffix(strings.TrimPrefix(line, "RCPT TO:<"), ">"))
				if config.reject == "recipient" && len(observed.recipients) == 2 {
					if !write("550 private-reply-recipient-rejected") {
						return
					}
				} else if !write("250 recipient accepted") {
					return
				}
			case line == "DATA":
				observed.dataCommands++
				if config.reject == "data" {
					if !write("554 private-reply-data-rejected") {
						return
					}
					continue
				}
				if !write("354 send data") {
					return
				}
				if config.dropBody {
					if tcp, ok := raw.(*net.TCPConn); ok {
						_ = tcp.SetLinger(0)
					}
					return
				}
				observed.data, err = io.ReadAll(reader.DotReader())
				if err != nil {
					observed.err = err
					return
				}
				if config.onData != nil {
					config.onData()
				}
				switch config.final {
				case "drop":
					return
				case "stall":
					_, observed.err = io.Copy(io.Discard, connection)
					return
				case "reject":
					if !write("554 private-reply-message-rejected") {
						return
					}
				case "temporary":
					if !write("451 private-reply-try-later") {
						return
					}
				default:
					if !write("250 accepted responsibility") {
						return
					}
				}
			case line == "QUIT":
				if config.onQuit != nil {
					config.onQuit()
				}
				if config.quit != "drop" {
					write("221 goodbye")
				}
				return
			default:
				if !write("500 unsupported") {
					return
				}
			}
		}
	}()
	return smtpProbe{listener.Addr().String(), root, results}
}

func (probe smtpProbe) config(security mail.SMTPSecurity) mail.SMTPConfig {
	config := mail.SMTPConfig{Address: probe.address, Security: security, Timeout: 5 * time.Second}
	if security != mail.SMTPPlaintext {
		config.ServerName = "mail.test"
		config.RootCAs = x509.NewCertPool()
		config.RootCAs.AddCert(probe.root)
		config.Username, config.Password = "probe-user", "probe-password"
	}
	return config
}

func (probe smtpProbe) result(t *testing.T) smtpObservation {
	t.Helper()
	select {
	case result := <-probe.results:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("SMTP probe did not finish")
		return smtpObservation{}
	}
}

func smtpSender(t *testing.T, config mail.SMTPConfig) *mail.SMTP {
	t.Helper()
	sender, err := mail.NewSMTP(config)
	if err != nil {
		t.Fatal(err)
	}
	return sender
}

func TestMailSMTPRealDeliveryTLSAndEnvelope(t *testing.T) {
	for _, security := range []mail.SMTPSecurity{mail.SMTPPlaintext, mail.SMTPStartTLS, mail.SMTPImplicitTLS} {
		t.Run(string(security), func(t *testing.T) {
			probe := startSMTPProbe(t, smtpProbeConfig{security: security})
			config := probe.config(security)
			config.LocalName = "::1"
			sender := smtpSender(t, config)
			// Configuration belongs to the constructor, not later caller edits.
			config.Username, config.Password, config.Address = "changed", "changed", "invalid"
			value := message(t, messageConfig(t))
			if err := sender.Send(t.Context(), value); err != nil {
				t.Fatal(err)
			}
			observed := probe.result(t)
			if observed.err != nil || observed.dataCommands != 1 || len(observed.mail) != 1 || len(observed.recipients) != 3 || observed.hello != "[IPv6:::1]" {
				t.Fatal("SMTP did not receive exactly one complete transaction", observed.err)
			}
			if observed.tls != (security != mail.SMTPPlaintext) || observed.authenticated != (security != mail.SMTPPlaintext) {
				t.Fatal("security mode or authenticated handoff was lost")
			}
			for index, recipient := range value.Recipients() {
				if observed.recipients[index] != recipient.Mailbox() {
					t.Fatal("envelope lost recipient")
				}
			}
			parsed, err := stdmail.ReadMessage(bytes.NewReader(observed.data))
			if err != nil || parsed.Header.Get("Bcc") != "" || parsed.Header.Get("Message-ID") == "" {
				t.Fatal("SMTP headers lost privacy or identity", err)
			}
			parts := parseParts(t, textproto.MIMEHeader(parsed.Header), parsed.Body)
			if len(parts) != 3 || parts[0].Text != value.Text() || parts[1].Text != value.HTML() || parts[2].Filename != "보고서.bin" {
				t.Fatal("SMTP dot-stuffing or MIME changed content")
			}
		})
	}
}

func TestMailSMTPRejectsDowngradeAndUntrustedCertificates(t *testing.T) {
	cases := []struct {
		name   string
		probe  smtpProbeConfig
		change func(*mail.SMTPConfig)
		code   mail.ErrorCode
	}{
		{"missing_starttls", smtpProbeConfig{security: mail.SMTPStartTLS, noStartTLS: true}, nil, mail.CodeUnsupported},
		{"missing_auth", smtpProbeConfig{security: mail.SMTPStartTLS, noAuth: true}, nil, mail.CodeUnsupported},
		{"auth_rejected", smtpProbeConfig{security: mail.SMTPStartTLS, reject: "auth"}, nil, mail.CodeRejected},
		{"untrusted_starttls", smtpProbeConfig{security: mail.SMTPStartTLS}, func(c *mail.SMTPConfig) { c.RootCAs = x509.NewCertPool() }, mail.CodeNotSent},
		{"untrusted_tls", smtpProbeConfig{security: mail.SMTPImplicitTLS}, func(c *mail.SMTPConfig) { c.RootCAs = x509.NewCertPool() }, mail.CodeNotSent},
		{"wrong_server", smtpProbeConfig{security: mail.SMTPImplicitTLS}, func(c *mail.SMTPConfig) { c.ServerName = "wrong.test" }, mail.CodeNotSent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := startSMTPProbe(t, tc.probe)
			config := probe.config(tc.probe.security)
			if tc.change != nil {
				tc.change(&config)
			}
			sender := smtpSender(t, config)
			if tc.name == "untrusted_tls" {
				// A constructor must own its trust roots. Mutating the original
				// empty pool after construction must not make this peer trusted.
				config.RootCAs.AddCert(probe.root)
			}
			err := sender.Send(t.Context(), message(t, messageConfig(t)))
			if !errors.Is(err, &mail.Error{Code: tc.code}) {
				t.Fatal("incorrect failure outcome", err)
			}
			observed := probe.result(t)
			if len(observed.mail) != 0 || observed.dataCommands != 0 || len(observed.data) != 0 {
				t.Fatal("failed security negotiation fell back to delivery")
			}
			if tc.name != "auth_rejected" && observed.authAttempt {
				t.Fatal("credentials sent without confirmed trust and capability")
			}
		})
	}
}

func TestMailSMTPRequiresUTF8Capability(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			probe := startSMTPProbe(t, smtpProbeConfig{smtpUTF8: enabled})
			config := messageConfig(t)
			config.To[0] = address(t, "사용자@example.test")
			err := smtpSender(t, probe.config(mail.SMTPPlaintext)).Send(t.Context(), message(t, config))
			observed := probe.result(t)
			if enabled {
				if err != nil || observed.dataCommands != 1 || observed.recipients[0] != "사용자@example.test" || !strings.Contains(observed.mail[0], " SMTPUTF8") {
					t.Fatal("advertised UTF8 transport lost original address", err)
				}
			} else if !errors.Is(err, &mail.Error{Code: mail.CodeUnsupported, Stage: "smtputf8"}) || len(observed.mail) != 0 {
				t.Fatal("unadvertised UTF8 transport accepted", err)
			}
		})
	}
}

func TestMailSMTPRejectionUnknownAndConfirmedOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		config smtpProbeConfig
		code   mail.ErrorCode
		data   int
	}{
		{"sender", smtpProbeConfig{reject: "mail"}, mail.CodeRejected, 0},
		{"recipient", smtpProbeConfig{reject: "recipient"}, mail.CodeRejected, 0},
		{"data_command", smtpProbeConfig{reject: "data"}, mail.CodeRejected, 1},
		{"final_rejection", smtpProbeConfig{final: "reject"}, mail.CodeRejected, 1},
		{"temporary_rejection", smtpProbeConfig{final: "temporary"}, mail.CodeRejected, 1},
		{"lost_acknowledgement", smtpProbeConfig{final: "drop"}, mail.CodeOutcomeUnknown, 1},
		{"quit_lost_after_acceptance", smtpProbeConfig{quit: "drop"}, "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := startSMTPProbe(t, tc.config)
			err := smtpSender(t, probe.config(mail.SMTPPlaintext)).Send(t.Context(), message(t, messageConfig(t)))
			if tc.code == "" {
				if err != nil {
					t.Fatal("confirmed acceptance was lost", err)
				}
			} else if !errors.Is(err, &mail.Error{Code: tc.code}) {
				t.Fatal("incorrect delivery outcome", err)
			}
			if strings.Contains(fmt.Sprintf("%+v", err), "private-reply") {
				t.Fatal("server reply leaked to diagnostics")
			}
			if tc.code == mail.CodeRejected {
				var reply *textproto.Error
				if !errors.As(err, &reply) || reply.Code < 400 {
					t.Fatal("explicit server rejection cause missing")
				}
			}
			observed := probe.result(t)
			if observed.dataCommands != tc.data {
				t.Fatal("DATA happened after a rejection or was retried")
			}
			if tc.name == "recipient" && (len(observed.recipients) != 2 || len(observed.data) != 0) {
				t.Fatal("partial recipient acceptance submitted message")
			}
		})
	}
}

func TestMailSMTPContextDeadlineAndResponseBound(t *testing.T) {
	for _, phase := range []string{"greeting", "data_acknowledgement", "after_acceptance", "timeout", "reply_limit", "body_write"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probeConfig := smtpProbeConfig{}
			code := mail.CodeNotSent
			switch phase {
			case "greeting":
				probeConfig.stallGreeting, probeConfig.onConnect = true, cancel
			case "data_acknowledgement":
				probeConfig.final, probeConfig.onData, code = "stall", cancel, mail.CodeOutcomeUnknown
			case "after_acceptance":
				probeConfig.quit, probeConfig.onQuit, code = "drop", cancel, ""
			case "timeout":
				probeConfig.stallGreeting = true
			case "reply_limit":
				probeConfig.hugeGreeting = true
			case "body_write":
				probeConfig.dropBody = true
			}
			probe := startSMTPProbe(t, probeConfig)
			config := probe.config(mail.SMTPPlaintext)
			if phase == "timeout" {
				config.Timeout = time.Second
			}
			input := messageConfig(t)
			if phase == "body_write" {
				input.Text = strings.Repeat("x", 8<<20)
			}
			err := smtpSender(t, config).Send(ctx, message(t, input))
			if code == "" {
				if err != nil || ctx.Err() == nil {
					t.Fatal("late cancellation revoked acceptance", err)
				}
			} else if !errors.Is(err, &mail.Error{Code: code}) {
				t.Fatal("incorrect interrupted send outcome", err)
			}
			if phase == "greeting" || phase == "data_acknowledgement" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("context cancellation cause lost", err)
				}
			}
			if phase == "timeout" {
				var timeout net.Error
				if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &timeout) && timeout.Timeout()) {
					t.Fatal("connection timeout lost", err)
				}
			}
			observed := probe.result(t)
			if (phase == "greeting" || phase == "timeout" || phase == "reply_limit") && len(observed.mail) != 0 {
				t.Fatal("early failure issued MAIL")
			}
		})
	}
}

func TestMailSMTPInvalidConfigurationAndPreCancelledCalls(t *testing.T) {
	for _, config := range []mail.SMTPConfig{
		{}, {Address: "localhost:25"}, {Address: "localhost:0", Security: mail.SMTPPlaintext},
		{Address: "localhost:70000", Security: mail.SMTPPlaintext}, {Address: "localhost:smtp", Security: mail.SMTPPlaintext},
		{Address: "localhost:25", Security: mail.SMTPPlaintext, Username: "user", Password: "password"},
		{Address: "localhost:25", Security: mail.SMTPImplicitTLS, Password: "password"},
		{Address: "localhost:25", Security: mail.SMTPImplicitTLS, Username: "user\x00other"},
		{Address: "localhost:25", Security: mail.SMTPPlaintext, Timeout: -1},
		{Address: "localhost:25", Security: mail.SMTPPlaintext, LocalName: "example\r\nMAIL FROM:<bad>"},
		{Address: "https://localhost:25", Security: mail.SMTPStartTLS},
	} {
		if _, err := mail.NewSMTP(config); !errors.Is(err, &mail.Error{Code: mail.CodeInvalidConfig}) {
			t.Fatal("invalid config admitted", err)
		}
	}
	sender := smtpSender(t, mail.SMTPConfig{Address: "127.0.0.1:1", Security: mail.SMTPPlaintext})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := sender.Send(ctx, message(t, messageConfig(t))); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled send attempted transport", err)
	}
	for _, err := range []error{sender.Send(nil, mail.Message{}), (*mail.SMTP)(nil).Send(t.Context(), mail.Message{}), sender.Send(t.Context(), mail.Message{})} {
		if err == nil {
			t.Fatal("invalid call accepted")
		}
	}
}
