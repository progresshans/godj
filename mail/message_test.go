package mail_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"net/textproto"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/mail"
)

var messageDate = time.Date(2026, 9, 28, 1, 2, 3, 0, time.UTC)

func address(t *testing.T, value string) mail.Address {
	t.Helper()
	result, err := mail.ParseAddress(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func messageConfig(t *testing.T) mail.MessageConfig {
	t.Helper()
	return mail.MessageConfig{
		From: address(t, `"Support Team" <sender@example.test>`),
		To:   []mail.Address{address(t, "member@example.test")}, Cc: []mail.Address{address(t, "copy@example.test")},
		Bcc: []mail.Address{address(t, "hidden@example.test")}, ReplyTo: []mail.Address{address(t, "reply@example.test")},
		Subject: "Reset 비밀번호 — end", Text: "Reset instructions: 비밀번호\n.\nend  \n", HTML: "<p>Reset <strong>비밀번호</strong></p>\n",
		Attachments: []mail.Attachment{{Filename: "보고서.bin", ContentType: "application/octet-stream", Data: []byte{0, 1, 13, 10, 255, 128, 65}}},
		Headers:     map[string]string{"X-Owner": "account"},
	}
}

func message(t *testing.T, config mail.MessageConfig) mail.Message {
	t.Helper()
	result, err := mail.NewMessage(config)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func wire(t *testing.T, value mail.Message) []byte {
	t.Helper()
	result, err := value.Bytes(messageDate, "<reference@example.test>")
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type mimePart struct {
	Type     string `json:"type"`
	Filename string `json:"filename"`
	Text     string `json:"text,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
}

type mimeObservation struct {
	Subject    string     `json:"subject"`
	From       []string   `json:"from"`
	To         []string   `json:"to"`
	Cc         []string   `json:"cc"`
	ReplyTo    []string   `json:"reply_to"`
	Recipients []string   `json:"recipients"`
	BccHeader  bool       `json:"bcc_header"`
	Extra      string     `json:"extra"`
	TopType    string     `json:"top_type"`
	LineLimit  bool       `json:"line_limit"`
	Parts      []mimePart `json:"parts"`
}

func parseParts(t *testing.T, header textproto.MIMEHeader, body io.Reader) []mimePart {
	t.Helper()
	kind, parameters, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(kind, "multipart/") {
		reader := multipart.NewReader(body, parameters["boundary"])
		var result []mimePart
		for {
			part, err := reader.NextRawPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, parseParts(t, part.Header, part)...)
			if err := part.Close(); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	switch header.Get("Content-Transfer-Encoding") {
	case "base64":
		body = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		body = quotedprintable.NewReader(body)
	case "", "7bit", "8bit":
	default:
		t.Fatal("unexpected transfer encoding")
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	part := mimePart{Type: kind}
	if disposition := header.Get("Content-Disposition"); disposition != "" {
		_, values, err := mime.ParseMediaType(disposition)
		if err != nil {
			t.Fatal(err)
		}
		part.Filename = values["filename"]
	}
	if strings.HasPrefix(kind, "text/") {
		part.Text = strings.ReplaceAll(string(data), "\r\n", "\n")
	} else {
		sum := sha256.Sum256(data)
		part.SHA256 = hex.EncodeToString(sum[:])
	}
	return []mimePart{part}
}

func observeMIME(t *testing.T, raw []byte, recipients []mail.Address) mimeObservation {
	t.Helper()
	parsed, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	decode := func(key string) string {
		value, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get(key))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	addresses := func(key string) []string {
		values, err := parsed.Header.AddressList(key)
		if err != nil {
			t.Fatal(err)
		}
		var result []string
		for _, value := range values {
			result = append(result, value.Address)
		}
		return result
	}
	result := mimeObservation{
		Subject: decode("Subject"), From: addresses("From"), To: addresses("To"), Cc: addresses("Cc"), ReplyTo: addresses("Reply-To"),
		BccHeader: parsed.Header.Get("Bcc") != "", Extra: decode("X-Owner"), LineLimit: true,
		Parts: parseParts(t, textproto.MIMEHeader(parsed.Header), parsed.Body),
	}
	result.TopType, _, err = mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	for _, recipient := range recipients {
		result.Recipients = append(result.Recipients, recipient.Mailbox())
	}
	for _, line := range bytes.Split(raw, []byte("\r\n")) {
		result.LineLimit = result.LineLimit && len(line) <= 998
	}
	date, err := parsed.Header.Date()
	if err != nil || !date.Equal(messageDate) || parsed.Header.Get("Message-ID") != "<reference@example.test>" {
		t.Fatal("delivery identity/date were not preserved")
	}
	return result
}

func TestMailDjangoReference(t *testing.T) {
	raw, err := os.ReadFile("testdata/django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Django       string `json:"django"`
		Observations struct {
			MIME     mimeObservation `json:"mime"`
			Envelope map[string]struct {
				Mailbox string `json:"mailbox"`
				Invalid bool   `json:"invalid"`
			} `json:"envelope"`
		} `json:"observations"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || fixture.Django != "6.1" || len(fixture.Observations.MIME.Parts) != 3 {
		t.Fatal("invalid independent native fixture", err)
	}
	value := message(t, messageConfig(t))
	got := observeMIME(t, wire(t, value), value.Recipients())
	if !reflect.DeepEqual(got, fixture.Observations.MIME) {
		t.Fatalf("independent MIME observation mismatch: got %+v, want %+v", got, fixture.Observations.MIME)
	}
	for name, input := range map[string]string{"idna": "user@bücher.example", "transitional": "user@faß.de", "quoted": `"a b"@example.test`, "multiple": "one@example.test,two@example.test"} {
		t.Run(name, func(t *testing.T) {
			actual, err := mail.ParseAddress(input)
			expected := fixture.Observations.Envelope[name]
			if (err != nil) != expected.Invalid || err == nil && actual.Mailbox() != expected.Mailbox {
				t.Fatal("native envelope observation mismatch", err)
			}
			if err == nil {
				again, err := mail.ParseAddress(actual.Header())
				if err != nil || again.Mailbox() != actual.Mailbox() {
					t.Fatal("mailbox was quoted more than once", err)
				}
			}
		})
	}
	// Deliberate transport extension: Unicode local parts remain unchanged,
	// and SMTP Send must require the server's explicit SMTPUTF8 capability.
	if !fixture.Observations.Envelope["unicode_local"].Invalid || address(t, "사용자@example.test").Mailbox() != "사용자@example.test" {
		t.Fatal("SMTPUTF8 deviation not explicit")
	}
}

func TestMailRejectsHeaderInjectionAndInvalidInputs(t *testing.T) {
	for _, input := range []string{"", "plain", "a@example.test\r\nBcc: victim@example.test", "a@example.test\nbad", "a\x00@example.test", "a@-bad.test", "a@bad_.test", "a@a..test", "a@\xff.test", "=?utf-8?b?DQpCLTogdHJ1ZQ==?= <a@example.test>", strings.Repeat("x", 65) + "@example.test"} {
		t.Run(fmt.Sprintf("address_%x", sha256.Sum256([]byte(input))), func(t *testing.T) {
			if _, err := mail.ParseAddress(input); !errors.Is(err, &mail.Error{Code: mail.CodeInvalidInput}) {
				t.Fatal("invalid address admitted", err)
			}
		})
	}
	cases := map[string]func(*mail.MessageConfig){
		"subject":             func(c *mail.MessageConfig) { c.Subject = "hello\r\nBcc: attacker@example.test" },
		"extra":               func(c *mail.MessageConfig) { c.Headers["X-Owner"] = "hello\nInjected: true" },
		"header_name":         func(c *mail.MessageConfig) { c.Headers["X-Owner: another"] = "hello" },
		"duplicate_header":    func(c *mail.MessageConfig) { c.Headers["x-owner"] = "other" },
		"bcc_override":        func(c *mail.MessageConfig) { c.Headers["bCc"] = "attacker@example.test" },
		"mime_override":       func(c *mail.MessageConfig) { c.Headers["content-type"] = "application/json" },
		"message_id_override": func(c *mail.MessageConfig) { c.Headers["Message-ID"] = "<bad@example.test>" },
		"missing_from":        func(c *mail.MessageConfig) { c.From = mail.Address{} },
		"missing_recipient":   func(c *mail.MessageConfig) { c.To, c.Cc, c.Bcc = nil, nil, nil },
		"invalid_recipient":   func(c *mail.MessageConfig) { c.Bcc[0] = mail.Address{} },
		"many_recipients": func(c *mail.MessageConfig) {
			for range mail.MaximumRecipients {
				c.To = append(c.To, c.From)
			}
		},
		"invalid_body":    func(c *mail.MessageConfig) { c.Text = "\xff" },
		"nul_body":        func(c *mail.MessageConfig) { c.HTML = "body\x00" },
		"large_body":      func(c *mail.MessageConfig) { c.Text = strings.Repeat("x", mail.MaximumMessageBytes) },
		"attachment_name": func(c *mail.MessageConfig) { c.Attachments[0].Filename = "a\r\nX-Hidden: yes" },
		"attachment_kind": func(c *mail.MessageConfig) { c.Attachments[0].ContentType = "multipart/mixed; boundary=bad" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			config := messageConfig(t)
			change(&config)
			if _, err := mail.NewMessage(config); !errors.Is(err, &mail.Error{Code: mail.CodeInvalidInput}) {
				t.Fatal("invalid message admitted", err)
			}
		})
	}
	value := message(t, messageConfig(t))
	for _, id := range []string{"", "<>", "<@>", "<a@>", "<@b>", "<a@b>\r\nBcc: bad", "<<a@b>>", "<a@b c>", "<한@b>", "<a..b@c>"} {
		if _, err := value.Bytes(messageDate, id); !errors.Is(err, &mail.Error{Code: mail.CodeInvalidInput}) {
			t.Fatal("invalid Message-ID admitted", err)
		}
	}
	for _, at := range []time.Time{
		time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("positive", 14*3600)),
		time.Date(9999, 12, 31, 23, 0, 0, 0, time.FixedZone("negative", -14*3600)),
	} {
		if _, err := value.Bytes(at, "<reference@example.test>"); !errors.Is(err, &mail.Error{Code: mail.CodeInvalidInput}) {
			t.Fatal("out-of-range UTC delivery date admitted", err)
		}
	}
}

func TestMailImmutableInputsAndDeterministicBytes(t *testing.T) {
	config := messageConfig(t)
	value := message(t, config)
	expected := wire(t, value)
	config.To[0], config.Bcc[0], config.ReplyTo[0] = config.From, config.From, config.From
	config.Attachments[0].Data[0] = 199
	config.Attachments[0].Filename = "changed"
	config.Headers["X-Owner"] = "changed"
	config.Subject, config.Text, config.HTML = "changed", "changed", "changed"
	if !bytes.Equal(expected, wire(t, value)) {
		t.Fatal("caller mutations changed immutable message")
	}
	copy := wire(t, value)
	copy[0] = 'X'
	recipients := value.Recipients()
	recipients[0] = config.From
	if !bytes.Equal(expected, wire(t, value)) || value.Recipients()[0].Mailbox() != "member@example.test" {
		t.Fatal("returned slices alias immutable storage")
	}
	for _, input := range []string{strings.Repeat("a", 4000), strings.Repeat("가🙂", 400), "spaces  remain  exact"} {
		config := messageConfig(t)
		config.Subject = input
		value := message(t, config)
		observed := observeMIME(t, wire(t, value), value.Recipients())
		if observed.Subject != input || !observed.LineLimit {
			t.Fatal("folding lost content or exceeded line limit")
		}
	}
	for _, text := range []string{"", "final byte", "long " + strings.Repeat("가", 1024), "line one\n.\nline two  \n", "line one\r\nline two\r\n"} {
		config := messageConfig(t)
		config.HTML, config.Attachments, config.Text = "", nil, text
		value := message(t, config)
		observed := observeMIME(t, wire(t, value), value.Recipients())
		if len(observed.Parts) != 1 || observed.Parts[0].Text != strings.ReplaceAll(text, "\r\n", "\n") || !observed.LineLimit {
			t.Fatal("plain body lost data during final flush or wrapping")
		}
	}
}

func TestMailDiagnosticsDoNotExposeMessageMaterial(t *testing.T) {
	config := messageConfig(t)
	config.Subject, config.Text, config.HTML = "private-marker-subject", "private-marker-token", "private-marker-html"
	config.Headers["X-Owner"] = "private-marker-header"
	config.Attachments[0].Data = []byte("private-marker-attachment")
	value := message(t, config)
	memory, err := mail.NewMemory(mail.MemoryConfig{})
	if err != nil || memory.Send(t.Context(), value) != nil {
		t.Fatal("prepare delivery diagnostics")
	}
	deliveries, _ := memory.Snapshot()
	_, parseErr := mail.ParseAddress("private-marker-invalid")
	if errors.Unwrap(parseErr) == nil {
		t.Fatal("explicit cause chain was lost")
	}
	smtpConfig := mail.SMTPConfig{Address: "127.0.0.1:25", Security: mail.SMTPStartTLS, Username: "private-marker-user", Password: "private-marker-password"}
	sender, err := mail.NewSMTP(smtpConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []any{config.From, config, config.Attachments[0], value, memory, deliveries[0], parseErr, smtpConfig, sender} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			printed := fmt.Sprintf(format, item)
			if strings.Contains(printed, "private-marker") || strings.Contains(printed, "example.test") {
				t.Fatal("format exposed secret material")
			}
		}
		encoded, err := json.Marshal(item)
		if err != nil || bytes.Contains(encoded, []byte("private-marker")) || bytes.Contains(encoded, []byte("example.test")) {
			t.Fatal("JSON exposed secret material", err)
		}
	}
}
