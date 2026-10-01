// Package mail provides immutable messages and explicit, context-aware delivery.
// Sending and recipient selection remain separate operations.
package mail

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/textproto"
	"strings"
	"unicode/utf8"
)

const MaximumMessageBytes = 16 << 20
const MaximumRecipients = 256

// Sender hands a complete message to a delivery backend. Nil means the backend
// accepted it, not that a recipient read or even received it. An unknown outcome
// must not be automatically retried: doing so could send duplicate messages.
// Implementations must be safe for concurrent use and must not retain contexts.
type Sender interface {
	Send(context.Context, Message) error
}

type Attachment struct {
	Filename, ContentType string
	Data                  []byte
}

func (Attachment) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("mail.Attachment{redacted}"))
}
func (Attachment) MarshalJSON() ([]byte, error) {
	return []byte(`"mail.Attachment{redacted}"`), nil
}

type MessageConfig struct {
	From                 Address
	To, Cc, Bcc, ReplyTo []Address
	Subject, Text, HTML  string
	Attachments          []Attachment
	// Headers contains extra unstructured fields. Envelope, MIME, date and
	// generated identity fields cannot be overridden. No raw folded input is
	// accepted; the renderer owns all encoding and folding.
	Headers map[string]string
}

func (MessageConfig) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("mail.MessageConfig{redacted}"))
}
func (MessageConfig) MarshalJSON() ([]byte, error) {
	return []byte(`"mail.MessageConfig{redacted}"`), nil
}

// Message owns every mutable input. Copies share an immutable value, while
// explicit recipient and byte accessors return caller-owned slices. Formatting
// and JSON do not disclose addresses, content, attachments or reset links.
type Message struct{ state *message }
type message struct {
	from                 Address
	to, cc, bcc, replyTo []Address
	subject, text, html  string
	attachments          []Attachment
	headers              map[string]string
	utf8                 bool
}

func (Message) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("mail.Message{redacted}"))
}
func (Message) MarshalJSON() ([]byte, error) { return []byte(`"mail.Message{redacted}"`), nil }

func NewMessage(config MessageConfig) (Message, error) {
	if config.From.state == nil || !headerText(config.Subject) || len(config.Subject) > 4096 ||
		!utf8.ValidString(config.Text) || !utf8.ValidString(config.HTML) ||
		strings.ContainsRune(config.Text, 0) || strings.ContainsRune(config.HTML, 0) {
		return Message{}, failure(CodeInvalidInput, "message", nil)
	}
	count := len(config.To) + len(config.Cc) + len(config.Bcc)
	if count < 1 || count > MaximumRecipients || len(config.ReplyTo) > MaximumRecipients ||
		len(config.Headers) > 64 || len(config.Attachments) > 64 {
		return Message{}, failure(CodeInvalidInput, "message", nil)
	}
	remaining := MaximumMessageBytes
	consume := func(size int) bool {
		if size > remaining {
			return false
		}
		remaining -= size
		return true
	}
	if !consume(len(config.Text)) || !consume(len(config.HTML)) || !consume(len(config.Subject)) || !consume(len(config.From.Header())) {
		return Message{}, failure(CodeInvalidInput, "message_size", nil)
	}
	state := &message{
		from: config.From, to: append([]Address(nil), config.To...), cc: append([]Address(nil), config.Cc...),
		bcc: append([]Address(nil), config.Bcc...), replyTo: append([]Address(nil), config.ReplyTo...),
		subject: config.Subject, text: config.Text, html: config.HTML, headers: map[string]string{}, utf8: config.From.state.smtpUTF8,
	}
	for _, group := range [][]Address{state.to, state.cc, state.bcc, state.replyTo} {
		for _, address := range group {
			if address.state == nil {
				return Message{}, failure(CodeInvalidInput, "recipient", nil)
			}
			state.utf8 = state.utf8 || address.state.smtpUTF8
			if !consume(len(address.Header())) {
				return Message{}, failure(CodeInvalidInput, "message_size", nil)
			}
		}
	}
	for name, value := range config.Headers {
		key := textproto.CanonicalMIMEHeaderKey(name)
		if !headerName(name) || !headerText(value) || len(value) > 4096 || reservedHeader(key) {
			return Message{}, failure(CodeInvalidInput, "header", nil)
		}
		if _, present := state.headers[key]; present {
			return Message{}, failure(CodeInvalidInput, "header", nil)
		}
		if !consume(len(key)) || !consume(len(value)) {
			return Message{}, failure(CodeInvalidInput, "message_size", nil)
		}
		state.headers[key] = value
	}
	for _, attachment := range config.Attachments {
		kind, params, err := mime.ParseMediaType(attachment.ContentType)
		if err != nil || !strings.Contains(kind, "/") || len(kind) > 255 || len(params) != 0 ||
			strings.HasPrefix(kind, "multipart/") || strings.HasPrefix(kind, "message/") ||
			!headerText(attachment.Filename) || attachment.Filename == "" || len(attachment.Filename) > 255 {
			return Message{}, failure(CodeInvalidInput, "attachment", nil)
		}
		if !consume(len(attachment.Data)) || !consume(len(attachment.Filename)) || !consume(len(kind)) {
			return Message{}, failure(CodeInvalidInput, "message_size", nil)
		}
		state.attachments = append(state.attachments, Attachment{attachment.Filename, kind, bytes.Clone(attachment.Data)})
	}
	return Message{state}, nil
}

func reservedHeader(name string) bool {
	switch name {
	case "From", "To", "Cc", "Bcc", "Reply-To", "Subject", "Date", "Message-Id", "Mime-Version", "Sender", "Return-Path":
		return true
	}
	return strings.HasPrefix(name, "Content-") || strings.HasPrefix(name, "Resent-")
}

func headerName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char < 33 || char > 126 || char == ':' {
			return false
		}
	}
	return true
}

func headerText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char < 32 || char == 127 {
			return false
		}
	}
	return true
}

func (m Message) From() Address {
	if m.state == nil {
		return Address{}
	}
	return m.state.from
}
func (m Message) Recipients() []Address {
	if m.state == nil {
		return nil
	}
	var result []Address
	for _, group := range [][]Address{m.state.to, m.state.cc, m.state.bcc} {
		result = append(result, group...)
	}
	return result
}
func (m Message) Subject() string {
	if m.state == nil {
		return ""
	}
	return m.state.subject
}
func (m Message) Text() string {
	if m.state == nil {
		return ""
	}
	return m.state.text
}
func (m Message) HTML() string {
	if m.state == nil {
		return ""
	}
	return m.state.html
}
