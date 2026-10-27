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
	Version            uint64 = 3
	DescribeArgument          = "__godj_first_account_describe_v3"
	ProvisionArgument         = "__godj_first_account_provision_v3"
	Magic                     = "GODJFA3"
	MaxPasswordBytes          = 4096
	MaxNameBytes              = 128
	requestHeaderBytes        = len(Magic) + 4 + 64 + 64 + 1 + 2
	MaxRequestBytes           = requestHeaderBytes + firstaccount.MaxFields*(4+MaxNameBytes) + firstaccount.MaxInputBytes + MaxPasswordBytes + firstaccount.MaxUploadBytes + firstaccount.MaxFields*(6+firstaccount.MaxUploadNameBytes)
)

var (
	ErrInvalidRequest  = errors.New("first account wire: invalid request")
	ErrInputTransport  = errors.New("first account wire: input transport failed")
	ErrInvalidResponse = errors.New("first account wire: invalid response")
	ErrOutputTransport = errors.New("first account wire: output transport failed")
)

// Request owns mutable scalar/file/password buffers. Clear releases them after one
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
		request.Values[i].Clear()
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
	if !validName(value.Name) || !value.Present && (len(value.Text) != 0 || value.Upload != nil) {
		return false
	}
	if value.Upload != nil {
		return len(value.Text) == 0 && firstaccount.ValidUploadName(value.Upload.Name) && len(value.Upload.Content) <= firstaccount.MaxUploadBytes
	}
	return len(value.Text) <= firstaccount.MaxFieldBytes && utf8.Valid(value.Text) && bytes.IndexByte(value.Text, 0) < 0
}

// EncodeRequest returns an independent frame. The wire only bounds raw password
// bytes; the selected account's actual password policy owns their meaning.
func EncodeRequest(request Request) ([]byte, error) {
	if !ValidDigest(request.Fingerprint) || !ValidDigest(request.OperationID) || len(request.Values) == 0 || len(request.Values) > firstaccount.MaxFields || len(request.Password) == 0 || len(request.Password) > MaxPasswordBytes {
		return nil, ErrInvalidRequest
	}
	size, inputBytes, fileBytes := requestHeaderBytes+len(request.Password), 0, 0
	names := make(map[string]bool, len(request.Values))
	for _, value := range request.Values {
		if !validValue(value) || names[value.Name] {
			return nil, ErrInvalidRequest
		}
		names[value.Name] = true
		inputBytes += len(value.Text)
		size += 4 + len(value.Name) + len(value.Text)
		if value.Upload != nil {
			if len(value.Upload.Content) > firstaccount.MaxUploadBytes-fileBytes {
				return nil, ErrInvalidRequest
			}
			fileBytes += len(value.Upload.Content)
			size += 6 + len(value.Upload.Name) + len(value.Upload.Content)
		}
	}
	if inputBytes > firstaccount.MaxInputBytes {
		return nil, ErrInvalidRequest
	}
	document := make([]byte, size)
	copy(document, Magic)
	offset := len(Magic)
	binary.BigEndian.PutUint32(document[offset:], uint32(size))
	offset += 4
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
			if value.Upload != nil {
				document[offset+1] = 2
			}
		}
		binary.BigEndian.PutUint16(document[offset+2:], uint16(len(value.Text)))
		offset += 4
		copy(document[offset:], value.Name)
		offset += len(value.Name)
		copy(document[offset:], value.Text)
		offset += len(value.Text)
		if value.Upload != nil {
			binary.BigEndian.PutUint16(document[offset:], uint16(len(value.Upload.Name)))
			binary.BigEndian.PutUint32(document[offset+2:], uint32(len(value.Upload.Content)))
			offset += 6
			copy(document[offset:], value.Upload.Name)
			offset += len(value.Upload.Name)
			copy(document[offset:], value.Upload.Content)
			offset += len(value.Upload.Content)
		}
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
	if binary.BigEndian.Uint32(document[offset:]) != uint32(len(document)) {
		return Request{}, ErrInvalidRequest
	}
	offset += 4
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
	inputBytes, fileBytes := 0, 0
	for i := range count {
		if len(document)-offset < 4 {
			return Request{}, ErrInvalidRequest
		}
		nameBytes, present, textBytes := int(document[offset]), document[offset+1], int(binary.BigEndian.Uint16(document[offset+2:]))
		offset += 4
		if present > 2 || nameBytes > MaxNameBytes || textBytes > firstaccount.MaxFieldBytes || len(document)-offset < nameBytes+textBytes {
			return Request{}, ErrInvalidRequest
		}
		value := firstaccount.Value{Name: string(document[offset : offset+nameBytes]), Present: present != 0, Text: document[offset+nameBytes : offset+nameBytes+textBytes]}
		if !validValue(value) || names[value.Name] {
			return Request{}, ErrInvalidRequest
		}
		names[value.Name] = true
		inputBytes += textBytes
		if inputBytes > firstaccount.MaxInputBytes {
			return Request{}, ErrInvalidRequest
		}
		offset += nameBytes + textBytes
		if present == 2 {
			if textBytes != 0 || len(document)-offset < 6 {
				return Request{}, ErrInvalidRequest
			}
			filenameBytes := int(binary.BigEndian.Uint16(document[offset:]))
			payloadBytes := binary.BigEndian.Uint32(document[offset+2:])
			offset += 6
			if filenameBytes > firstaccount.MaxUploadNameBytes || payloadBytes > uint32(firstaccount.MaxUploadBytes-fileBytes) || len(document)-offset < filenameBytes+int(payloadBytes) {
				return Request{}, ErrInvalidRequest
			}
			value.Upload = &firstaccount.Upload{Name: string(document[offset : offset+filenameBytes]), Content: document[offset+filenameBytes : offset+filenameBytes+int(payloadBytes)]}
			if !validValue(value) {
				return Request{}, ErrInvalidRequest
			}
			fileBytes += int(payloadBytes)
			offset += filenameBytes + int(payloadBytes)
		}
		views[i] = value
	}
	if len(document)-offset != passwordBytes {
		return Request{}, ErrInvalidRequest
	}
	request := Request{Fingerprint: fingerprint, OperationID: operationID, Values: make([]firstaccount.Value, count), Password: bytes.Clone(document[offset:])}
	for i := range count {
		request.Values[i] = views[i]
		request.Values[i].Text = bytes.Clone(views[i].Text)
		if views[i].Upload != nil {
			request.Values[i].Upload = &firstaccount.Upload{Name: views[i].Upload.Name, Content: bytes.Clone(views[i].Upload.Content)}
		}
	}
	return request, nil
}

// ReadRequest requires the declared bounded frame and EOF. It allocates only
// the announced size after validating the prefix; scalar requests never reserve
// the upload ceiling. Temporary buffers are cleared on every exit or panic.
// The process owner joins reads and owns cancellation of a blocked process.
func ReadRequest(reader io.Reader) (request Request, err error) {
	var document []byte
	defer func() {
		clear(document)
		if recover() != nil {
			request.Clear()
			err = ErrInputTransport
		}
	}()
	if reader == nil {
		return Request{}, ErrInputTransport
	}
	prefixBytes := len(Magic) + 4
	document = make([]byte, prefixBytes)
	used, emptyReads, expected := 0, 0, 0
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
		if expected == 0 && used == prefixBytes {
			if !bytes.Equal(document[:len(Magic)], []byte(Magic)) {
				return Request{}, ErrInvalidRequest
			}
			announced := binary.BigEndian.Uint32(document[len(Magic):])
			if announced < uint32(requestHeaderBytes) || announced > uint32(MaxRequestBytes) {
				return Request{}, ErrInvalidRequest
			}
			expected = int(announced)
			enlarged := make([]byte, expected+1)
			copy(enlarged, document)
			clear(document)
			document = enlarged
		} else if expected != 0 && used > expected {
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
