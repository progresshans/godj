package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Bytes renders MIME without transport I/O using an explicit delivery date and
// Message-ID. The result is caller-owned secret material, never diagnostics.
// Equal immutable inputs render equal bytes; transport Send supplies a fresh ID.
func (m Message) Bytes(at time.Time, messageID string) ([]byte, error) {
	at = at.UTC()
	if m.state == nil || at.Year() < 1 || at.Year() > 9999 || !validMessageID(messageID) {
		return nil, failure(CodeInvalidInput, "message_identity", nil)
	}
	var output bytes.Buffer
	header := func(name, value string) { output.WriteString(name + ": " + value + "\r\n") }
	header("From", m.state.from.Header())
	for _, group := range []struct {
		name   string
		values []Address
	}{{"To", m.state.to}, {"Cc", m.state.cc}, {"Reply-To", m.state.replyTo}} {
		if len(group.values) != 0 {
			values := make([]string, len(group.values))
			for index, value := range group.values {
				values[index] = value.Header()
			}
			header(group.name, strings.Join(values, ",\r\n "))
		}
	}
	header("Subject", encodedWords(m.state.subject))
	header("Date", at.Format(time.RFC1123Z))
	header("Message-ID", messageID)
	header("MIME-Version", "1.0")
	keys := make([]string, 0, len(m.state.headers))
	for key := range m.state.headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		header(key, encodedWords(m.state.headers[key]))
	}
	bodyType, body, err := m.body()
	if err != nil {
		return nil, failure(CodeInvalidInput, "mime", err)
	}
	header("Content-Type", bodyType)
	if m.state.html == "" && len(m.state.attachments) == 0 {
		header("Content-Transfer-Encoding", "quoted-printable")
	}
	output.WriteString("\r\n")
	output.Write(body)
	if output.Len() > 4*MaximumMessageBytes {
		return nil, failure(CodeInvalidInput, "message_size", nil)
	}
	for _, line := range bytes.Split(output.Bytes(), []byte("\r\n")) {
		if len(line) > 998 {
			return nil, failure(CodeInvalidInput, "line_length", nil)
		}
	}
	return output.Bytes(), nil
}

func validMessageID(value string) bool {
	if len(value) > 254 || !strings.HasPrefix(value, "<") || !strings.HasSuffix(value, ">") || strings.Count(value, "@") != 1 {
		return false
	}
	parts := strings.Split(value[1:len(value)-1], "@")
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.Contains(part, "..") {
			return false
		}
		for _, char := range part {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune(".!#$%&'*+-/=?^_`{|}~", char)) {
				return false
			}
		}
	}
	return true
}

// Encoded words preserve even long whitespace-free ASCII subjects without
// exceeding RFC 2047's 75-byte word limit. Break only at UTF-8 rune boundaries.
func encodedWords(value string) string {
	var words []string
	for len(value) > 0 {
		size := min(42, len(value))
		for size < len(value) && !utf8.RuneStart(value[size]) {
			size--
		}
		words = append(words, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(value[:size]))+"?=")
		value = value[size:]
	}
	return strings.Join(words, "\r\n ")
}

func quoted(value string) ([]byte, error) {
	var output bytes.Buffer
	writer := quotedprintable.NewWriter(&output)
	_, err := writer.Write([]byte(value))
	err = errors.Join(err, writer.Close())
	return output.Bytes(), err
}

func multipartWriter(output *bytes.Buffer, domain string, content []byte) (*multipart.Writer, string, error) {
	writer := multipart.NewWriter(output)
	sum := sha256.Sum256(append([]byte("godj.mail."+domain+"\x00"), content...))
	boundary := "godj-" + hex.EncodeToString(sum[:])
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, "", err
	}
	return writer, mime.FormatMediaType("multipart/"+domain, map[string]string{"boundary": boundary}), nil
}

func (m Message) body() (string, []byte, error) {
	content, err := quoted(m.state.text)
	if err != nil {
		return "", nil, err
	}
	kind := "text/plain; charset=utf-8"
	if m.state.html != "" {
		var output bytes.Buffer
		writer, contentType, err := multipartWriter(&output, "alternative", append(bytes.Clone(content), []byte(m.state.html)...))
		if err != nil {
			return "", nil, err
		}
		for _, part := range []struct{ kind, text string }{{"text/plain", m.state.text}, {"text/html", m.state.html}} {
			header := textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}}
			body, err := writer.CreatePart(header)
			if err != nil {
				return "", nil, err
			}
			value, err := quoted(part.text)
			if err != nil {
				return "", nil, err
			}
			if _, err = body.Write(value); err != nil {
				return "", nil, err
			}
		}
		if err := writer.Close(); err != nil {
			return "", nil, err
		}
		content, kind = output.Bytes(), contentType
	}
	if len(m.state.attachments) != 0 {
		var output bytes.Buffer
		writer, contentType, err := multipartWriter(&output, "mixed", content)
		if err != nil {
			return "", nil, err
		}
		header := textproto.MIMEHeader{"Content-Type": {kind}}
		if m.state.html == "" {
			header.Set("Content-Transfer-Encoding", "quoted-printable")
		}
		body, err := writer.CreatePart(header)
		if err != nil {
			return "", nil, err
		}
		if _, err = body.Write(content); err != nil {
			return "", nil, err
		}
		for _, attachment := range m.state.attachments {
			header := textproto.MIMEHeader{
				"Content-Type": {attachment.ContentType}, "Content-Transfer-Encoding": {"base64"},
				"Content-Disposition": {mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Filename})},
			}
			body, err := writer.CreatePart(header)
			if err != nil {
				return "", nil, err
			}
			encoded := base64.StdEncoding.EncodeToString(attachment.Data)
			for len(encoded) > 0 {
				size := min(76, len(encoded))
				if _, err := body.Write([]byte(encoded[:size] + "\r\n")); err != nil {
					return "", nil, err
				}
				encoded = encoded[size:]
			}
		}
		if err := writer.Close(); err != nil {
			return "", nil, err
		}
		content, kind = output.Bytes(), contentType
	}
	return kind, content, nil
}

func prepare(ctx context.Context, message Message) (Delivery, error) {
	if ctx == nil {
		return Delivery{}, failure(CodeInvalidInput, "context", nil)
	}
	if err := ctx.Err(); err != nil {
		return Delivery{}, failure(CodeNotSent, "context", err)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Delivery{}, failure(CodeNotSent, "message_identity", err)
	}
	at, identity := time.Now().UTC(), "<"+hex.EncodeToString(id[:])+"@godj.invalid>"
	data, err := message.Bytes(at, identity)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return Delivery{}, failure(CodeNotSent, "message", err)
	}
	return Delivery{&delivery{message, at, identity, data}}, nil
}
