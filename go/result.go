package mpesa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ResultCallback is the result Daraja POSTs to the ResultURL configured for
// the initiator APIs (B2C, TransactionStatus, AccountBalance, Reversal).
// The generic result parameters are flattened into Parameters, keyed by
// their Daraja names (e.g. "TransactionAmount", "TransactionReceipt",
// "B2CWorkingAccountAvailableFunds", "AvailableBalance") — values arrive as
// JSON strings or numbers and are rendered as plain text.
type ResultCallback struct {
	ResultType              int
	ResultCode              int
	ResultDesc              string
	OriginatorConversationID string
	ConversationID          string
	TransactionID           string // M-Pesa receipt, when applicable
	Parameters              map[string]string
}

// Success reports whether the operation completed (ResultCode 0).
func (rc *ResultCallback) Success() bool { return rc.ResultCode == ResultSuccess }

type rawResultCallback struct {
	Result struct {
		ResultType               int    `json:"ResultType"`
		ResultCode               int    `json:"ResultCode"`
		ResultDesc               string `json:"ResultDesc"`
		OriginatorConversationID string `json:"OriginatorConversationID"`
		ConversationID           string `json:"ConversationID"`
		TransactionID            string `json:"TransactionID"`
		ResultParameters         *struct {
			ResultParameter []struct {
				Key   string          `json:"Key"`
				Value json.RawMessage `json:"Value"`
			} `json:"ResultParameter"`
		} `json:"ResultParameters"`
	} `json:"Result"`
}

// ParseResultCallback decodes the body Safaricom posts to a ResultURL.
func ParseResultCallback(r io.Reader) (*ResultCallback, error) {
	var raw rawResultCallback
	if err := json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&raw); err != nil {
		return nil, err
	}
	res := raw.Result
	if res.ConversationID == "" && res.OriginatorConversationID == "" {
		return nil, errors.New("mpesa: result callback missing conversation IDs")
	}
	rc := &ResultCallback{
		ResultType:               res.ResultType,
		ResultCode:               res.ResultCode,
		ResultDesc:               res.ResultDesc,
		OriginatorConversationID: res.OriginatorConversationID,
		ConversationID:           res.ConversationID,
		TransactionID:            res.TransactionID,
		Parameters:               map[string]string{},
	}
	if res.ResultParameters != nil {
		for _, p := range res.ResultParameters.ResultParameter {
			rc.Parameters[p.Key] = rawToString(p.Value)
		}
	}
	return rc, nil
}

// ResultHandler returns an http.Handler for the ResultURL of the initiator
// APIs. It parses the body, calls fn, and replies in the format Daraja
// expects. Result callbacks are not signed; as with STK callbacks, make fn
// idempotent (dedupe on ConversationID / TransactionID).
func ResultHandler(fn func(ctx context.Context, rc *ResultCallback) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rc, err := ParseResultCallback(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := fn(r.Context(), rc); err != nil {
			http.Error(w, "processing error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ResultCode":0,"ResultDesc":"Accepted"}`))
	})
}
