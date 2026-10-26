// Package firstaccountwire owns the current, bounded selected-account process
// protocol. The parent retains the operation ID before handing off any input.
// It never transports submitted values through argv, environment or JSON.
package firstaccountwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/project/firstaccount"
)

const (
	Version            uint64 = 2
	DescribeArgument          = "__godj_first_account_describe_v2"
	ProvisionArgument         = "__godj_first_account_provision_v2"
	Magic                     = "GODJFA2"
	MaxPasswordBytes          = 4096
	MaxNameBytes              = 128
	requestHeaderBytes        = len(Magic) + 64 + 64 + 1 + 2
	MaxRequestBytes           = requestHeaderBytes + firstaccount.MaxFields*(4+MaxNameBytes) + firstaccount.MaxInputBytes + MaxPasswordBytes
)

var (
	ErrInvalidRequest  = errors.New("first account wire: invalid request")
	ErrInputTransport  = errors.New("first account wire: input transport failed")
	ErrInvalidResponse = errors.New("first account wire: invalid response")
	ErrOutputTransport = errors.New("first account wire: output transport failed")
)

// Request owns mutable scalar/password buffers. Clear releases them after one
// attempt. Formatting and JSON never expose the input or the declaration.
type Request struct {
	Fingerprint string
	OperationID string
	Values      []firstaccount.Value
	Password    []byte
}

func (Request) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("firstaccountwire.Request{redacted}"))
}
func (Request) MarshalJSON() ([]byte, error) {
	return []byte(`"firstaccountwire.Request{redacted}"`), nil
}

func (request *Request) Clear() {
	if request == nil {
		return
	}
	for i := range request.Values {
		clear(request.Values[i].Text)
		request.Values[i] = firstaccount.Value{}
	}
	clear(request.Password)
	*request = Request{}
}

func ValidDigest(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == "" && strings.Trim(value, "0") != ""
}

func validName(value string) bool {
	return len(value) > 0 && len(value) <= MaxNameBytes && strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") == ""
}

func validValue(value firstaccount.Value) bool {
	return validName(value.Name) && (value.Present || len(value.Text) == 0) && len(value.Text) <= firstaccount.MaxFieldBytes && utf8.Valid(value.Text) && bytes.IndexByte(value.Text, 0) < 0
}

// EncodeRequest returns an independent frame. The wire only bounds raw password
// bytes; the selected account's actual password policy owns their meaning.
func EncodeRequest(request Request) ([]byte, error) {
	if !ValidDigest(request.Fingerprint) || !ValidDigest(request.OperationID) || len(request.Values) == 0 || len(request.Values) > firstaccount.MaxFields || len(request.Password) == 0 || len(request.Password) > MaxPasswordBytes {
		return nil, ErrInvalidRequest
	}
	size, inputBytes := requestHeaderBytes+len(request.Password), 0
	names := make(map[string]bool, len(request.Values))
	for _, value := range request.Values {
		if !validValue(value) || names[value.Name] {
			return nil, ErrInvalidRequest
		}
		names[value.Name] = true
		inputBytes += len(value.Text)
		size += 4 + len(value.Name) + len(value.Text)
	}
	if inputBytes > firstaccount.MaxInputBytes {
		return nil, ErrInvalidRequest
	}
	document := make([]byte, size)
	copy(document, Magic)
	offset := len(Magic)
	copy(document[offset:], request.Fingerprint)
	offset += 64
	copy(document[offset:], request.OperationID)
	offset += 64
	document[offset] = byte(len(request.Values))
	offset++
	binary.BigEndian.PutUint16(document[offset:], uint16(len(request.Password)))
	offset += 2
	for _, value := range request.Values {
		document[offset] = byte(len(value.Name))
		if value.Present {
			document[offset+1] = 1
		}
		binary.BigEndian.PutUint16(document[offset+2:], uint16(len(value.Text)))
		offset += 4
		copy(document[offset:], value.Name)
		offset += len(value.Name)
		copy(document[offset:], value.Text)
		offset += len(value.Text)
	}
	copy(document[offset:], request.Password)
	return document, nil
}

// DecodeRequest validates the whole frame before allocating detached input
// buffers. In particular omission, exact field order, empty text and raw
// password bytes are not normalized by a second input policy.
func DecodeRequest(document []byte) (Request, error) {
	if len(document) < requestHeaderBytes || len(document) > MaxRequestBytes || !bytes.Equal(document[:len(Magic)], []byte(Magic)) {
		return Request{}, ErrInvalidRequest
	}
	offset := len(Magic)
	fingerprint := string(document[offset : offset+64])
	offset += 64
	operationID := string(document[offset : offset+64])
	offset += 64
	count := int(document[offset])
	offset++
	passwordBytes := int(binary.BigEndian.Uint16(document[offset:]))
	offset += 2
	if !ValidDigest(fingerprint) || !ValidDigest(operationID) || count < 1 || count > firstaccount.MaxFields || passwordBytes < 1 || passwordBytes > MaxPasswordBytes {
		return Request{}, ErrInvalidRequest
	}
	var views [firstaccount.MaxFields]firstaccount.Value
	names := make(map[string]bool, count)
	inputBytes := 0
	for i := range count {
		if len(document)-offset < 4 {
			return Request{}, ErrInvalidRequest
		}
		nameBytes, present, textBytes := int(document[offset]), document[offset+1], int(binary.BigEndian.Uint16(document[offset+2:]))
		offset += 4
		if present > 1 || nameBytes > MaxNameBytes || textBytes > firstaccount.MaxFieldBytes || len(document)-offset < nameBytes+textBytes {
			return Request{}, ErrInvalidRequest
		}
		value := firstaccount.Value{Name: string(document[offset : offset+nameBytes]), Present: present == 1, Text: document[offset+nameBytes : offset+nameBytes+textBytes]}
		if !validValue(value) || names[value.Name] {
			return Request{}, ErrInvalidRequest
		}
		names[value.Name] = true
		inputBytes += textBytes
		if inputBytes > firstaccount.MaxInputBytes {
			return Request{}, ErrInvalidRequest
		}
		views[i] = value
		offset += nameBytes + textBytes
	}
	if len(document)-offset != passwordBytes {
		return Request{}, ErrInvalidRequest
	}
	request := Request{Fingerprint: fingerprint, OperationID: operationID, Values: make([]firstaccount.Value, count), Password: bytes.Clone(document[offset:])}
	for i := range count {
		request.Values[i] = views[i]
		request.Values[i].Text = bytes.Clone(views[i].Text)
	}
	return request, nil
}

// ReadRequest requires EOF and retains at most one byte beyond the frame bound.
// Every temporary secret buffer is cleared, including reader failure/panic.
// Its process owner joins the read and owns cancellation of a blocked process.
func ReadRequest(reader io.Reader) (request Request, err error) {
	defer func() {
		if recover() != nil {
			request.Clear()
			err = ErrInputTransport
		}
	}()
	if reader == nil {
		return Request{}, ErrInputTransport
	}
	document := make([]byte, MaxRequestBytes+1)
	defer clear(document)
	used, emptyReads := 0, 0
	for {
		n, readErr := reader.Read(document[used:])
		if n < 0 || n > len(document)-used {
			return Request{}, ErrInputTransport
		}
		used += n
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return Request{}, ErrInputTransport
			}
			return DecodeRequest(document[:used])
		}
		if used > MaxRequestBytes {
			return Request{}, ErrInvalidRequest
		}
		if n == 0 {
			emptyReads++
			if emptyReads == 100 {
				return Request{}, ErrInputTransport
			}
		} else {
			emptyReads = 0
		}
	}
}
