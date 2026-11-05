package mpesa

import (
	"errors"
	"fmt"
)

// codeTransactionProcessing is returned by the STK query endpoint while the
// customer has not yet completed (or cancelled) the prompt.
const codeTransactionProcessing = "500.001.1001"

// Well-known STK result codes, as delivered in callbacks (int) and STKQuery
// responses (string). This list covers the codes every integration branches
// on; see Daraja's docs for the full set.
const (
	ResultSuccess           = 0    // The service request is processed successfully.
	ResultInsufficientFunds = 1    // The customer's M-Pesa balance is too low.
	ResultCancelledByUser   = 1032 // Request cancelled by user.
	ResultUnreachable       = 1037 // DS timeout: the user could not be reached.
)

// APIError is returned when Daraja responds with an error.
type APIError struct {
	HTTPStatus int
	RequestID  string
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("mpesa: api error (http %d, code %s, request %s): %s",
		e.HTTPStatus, e.Code, e.RequestID, e.Message)
}

// IsProcessing reports whether err means "transaction still being processed".
// When polling STKQuery, treat this as "try again shortly", not as a failure.
func IsProcessing(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == codeTransactionProcessing
}

// RequestError is returned when Daraja accepts the HTTP request (status 200)
// but rejects the operation itself — a non-"0" ResponseCode in the body, e.g.
// an STK push rejection or a failed B2C request. The identifiers are included
// so callers can correlate the rejection with their own records.
type RequestError struct {
	ResponseCode        string
	ResponseDescription string
	MerchantRequestID   string
	CheckoutRequestID   string
}

func (e *RequestError) Error() string {
	if e.ResponseDescription == "" {
		return fmt.Sprintf("mpesa: request rejected (code %s)", e.ResponseCode)
	}
	return fmt.Sprintf("mpesa: request rejected: %s (%s)", e.ResponseDescription, e.ResponseCode)
}

// IsRejected reports whether err is a Daraja-side rejection of the request
// (as opposed to a transport error or a validation error).
func IsRejected(err error) bool {
	var re *RequestError
	return errors.As(err, &re)
}
