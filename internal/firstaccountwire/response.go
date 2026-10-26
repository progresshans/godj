package firstaccountwire

import (
	"encoding/json"
	"io"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/project/firstaccount"
)

const (
	MaxDescriptionResponseBytes                = firstaccount.MaxDescriptionBytes + 512
	MaxResponseBytes                           = 4096
	KnownCreatedResponseFailureExitCode        = 86
	KnownCreatedCleanupResponseFailureExitCode = 87
)

type Code string

const (
	InvalidConfig        Code = "invalid_config"
	InvalidRequest       Code = "invalid_request"
	InvalidInput         Code = "invalid_input"
	InputTransportFailed Code = "input_transport_failed"
	OpenFailed           Code = "backend_open_failed"
	NotFresh             Code = "account_domain_not_fresh"
	PersistenceFailed    Code = "persistence_failed"
	OutcomeUnknown       Code = "outcome_unknown"
	Canceled             Code = "canceled"
	InternalError        Code = "internal_error"
)

type Outcome string

const (
	NotCreated Outcome = "not_created"
	Created    Outcome = "created"
	Unknown    Outcome = "unknown"
)

// Response contains only durable outcome and diagnostic metadata. CleanupFailed
// cannot erase either a confirmed commit or a reconciliation requirement.
// A response with no operation ID is valid only for a rejected request frame.
type Response struct {
	Version       uint64  `json:"version"`
	OperationID   string  `json:"operation_id"`
	Outcome       Outcome `json:"outcome"`
	Code          Code    `json:"code"`
	CleanupFailed bool    `json:"cleanup_failed"`
}

func validResponse(response Response) bool {
	if response.Version != Version {
		return false
	}
	if response.OperationID == "" {
		return response.Outcome == NotCreated && !response.CleanupFailed && (response.Code == InvalidRequest || response.Code == InputTransportFailed || response.Code == Canceled)
	}
	if !ValidDigest(response.OperationID) {
		return false
	}
	switch response.Outcome {
	case Created:
		return response.Code == ""
	case Unknown:
		return response.Code == OutcomeUnknown
	case NotCreated:
		switch response.Code {
		case InvalidConfig, InvalidInput, OpenFailed, NotFresh, PersistenceFailed, Canceled, InternalError:
			return true
		}
	}
	return false
}

func EncodeResponse(response Response) ([]byte, error) {
	if !validResponse(response) {
		return nil, ErrInvalidResponse
	}
	document, err := json.Marshal(response)
	if err != nil || len(document) > MaxResponseBytes {
		return nil, ErrInvalidResponse
	}
	return document, nil
}

// DecodeResponse accepts the sole canonical response and binds it to this
// parent's operation. An empty operation is only a pre-acquisition rejection.
func DecodeResponse(document []byte, operationID string) (Response, error) {
	var response Response
	if !ValidDigest(operationID) || wirejson.Scan(document, wirejson.Limits{Bytes: MaxResponseBytes, Containers: 1, Values: 7, ObjectKeys: 6, KeyBytes: 32, StringBytes: 128, RejectArrays: true, RejectNull: true}) != nil || wirejson.DecodeCanonical(document, &response) != nil || !validResponse(response) || response.OperationID != "" && response.OperationID != operationID {
		return Response{}, ErrInvalidResponse
	}
	return response, nil
}

type DescriptionResponse struct {
	Version     uint64                    `json:"version"`
	Description *firstaccount.Description `json:"description"`
	Code        Code                      `json:"code"`
}

func validDescriptionResponse(response DescriptionResponse) bool {
	if response.Version != Version {
		return false
	}
	if response.Description != nil {
		return response.Code == "" && firstaccount.ValidateDescription(*response.Description) == nil
	}
	return response.Code == InvalidConfig || response.Code == Canceled || response.Code == InternalError
}

func EncodeDescription(response DescriptionResponse) ([]byte, error) {
	if !validDescriptionResponse(response) {
		return nil, ErrInvalidResponse
	}
	document, err := json.Marshal(response)
	if err != nil || len(document) > MaxDescriptionResponseBytes {
		return nil, ErrInvalidResponse
	}
	return document, nil
}

// The canonical typed description is preflighted before allocation; arbitrary
// keys, duplicate fields, lossy Unicode, missing fields and extra JSON fail.
func DecodeDescription(document []byte) (DescriptionResponse, error) {
	var response DescriptionResponse
	if wirejson.Scan(document, wirejson.Limits{Bytes: MaxDescriptionResponseBytes, Containers: 6, Values: MaxDescriptionResponseBytes, ObjectKeys: 16, ArrayValues: firstaccount.MaxChoices, StringBytes: firstaccount.MaxFieldBytes, KeyBytes: 32}) != nil || wirejson.DecodeCanonical(document, &response) != nil || !validDescriptionResponse(response) {
		return DescriptionResponse{}, ErrInvalidResponse
	}
	return response, nil
}

// Write makes one bounded attempt. The process owner joins arbitrary writers
// and owns panic/Goexit; output errors never include writer-supplied text.
func Write(writer io.Writer, document []byte) error {
	if writer == nil || len(document) == 0 || len(document) > MaxDescriptionResponseBytes {
		return ErrOutputTransport
	}
	n, err := writer.Write(document)
	if err != nil || n != len(document) {
		return ErrOutputTransport
	}
	return nil
}
