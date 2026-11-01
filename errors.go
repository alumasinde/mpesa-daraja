package mpesa

import (
	"errors"
	"fmt"
)

// codeTransactionProcessing is returned by the STK query endpoint while the
// customer has not yet completed (or cancelled) the prompt.
const codeTransactionProcessing = "500.001.1001"

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
