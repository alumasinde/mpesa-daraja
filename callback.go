package mpesa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// STKCallback is the parsed result of an STK push, delivered to your
// CallbackURL. On failure/cancel only the identifiers, ResultCode and
// ResultDesc are set.
type STKCallback struct {
	MerchantRequestID  string
	CheckoutRequestID  string
	ResultCode         int
	ResultDesc         string
	Amount             float64
	MpesaReceiptNumber string
	TransactionDate    time.Time
	PhoneNumber        string
}

// Success reports whether the customer paid.
func (cb *STKCallback) Success() bool { return cb.ResultCode == 0 }

type rawSTKCallback struct {
	Body struct {
		STKCallback struct {
			MerchantRequestID string `json:"MerchantRequestID"`
			CheckoutRequestID string `json:"CheckoutRequestID"`
			ResultCode        int    `json:"ResultCode"`
			ResultDesc        string `json:"ResultDesc"`
			CallbackMetadata  *struct {
				Item []struct {
					Name  string          `json:"Name"`
					Value json.RawMessage `json:"Value"`
				} `json:"Item"`
			} `json:"CallbackMetadata"`
		} `json:"stkCallback"`
	} `json:"Body"`
}

// ParseSTKCallback decodes the body Safaricom posts to your STK CallbackURL.
func ParseSTKCallback(r io.Reader) (*STKCallback, error) {
	var raw rawSTKCallback
	if err := json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("mpesa: bad callback body: %w", err)
	}
	s := raw.Body.STKCallback
	if s.CheckoutRequestID == "" {
		return nil, errors.New("mpesa: callback missing CheckoutRequestID")
	}

	cb := &STKCallback{
		MerchantRequestID: s.MerchantRequestID,
		CheckoutRequestID: s.CheckoutRequestID,
		ResultCode:        s.ResultCode,
		ResultDesc:        s.ResultDesc,
	}
	if s.CallbackMetadata == nil {
		return cb, nil
	}
	for _, it := range s.CallbackMetadata.Item {
		v := rawToString(it.Value)
		switch it.Name {
		case "Amount":
			cb.Amount, _ = strconv.ParseFloat(v, 64)
		case "MpesaReceiptNumber":
			cb.MpesaReceiptNumber = v
		case "TransactionDate":
			if t, err := time.ParseInLocation("20060102150405", v, eat); err == nil {
				cb.TransactionDate = t
			}
		case "PhoneNumber":
			cb.PhoneNumber = v
		}
	}
	return cb, nil
}

// rawToString renders a JSON string or number as plain text without
// float rounding (phone numbers and dates arrive as numbers).
func rawToString(v json.RawMessage) string {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(v))
}

// CallbackHandler returns an http.Handler for your STK CallbackURL. It parses
// the body, calls fn, and replies in the format Daraja expects.
//
// Safaricom may deliver the same callback more than once, so fn must be
// idempotent: dedupe on CheckoutRequestID. Callbacks are not signed; confirm
// important payments with STKQuery and/or restrict source IPs at your proxy.
func CallbackHandler(fn func(ctx context.Context, cb *STKCallback) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cb, err := ParseSTKCallback(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := fn(r.Context(), cb); err != nil {
			http.Error(w, "processing error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ResultCode":0,"ResultDesc":"Accepted"}`))
	})
}
