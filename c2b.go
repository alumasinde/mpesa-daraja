package mpesa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ResponseType tells M-Pesa what to do when your validation URL is
// unreachable: complete the payment anyway, or cancel it.
type ResponseType string

const (
	// ResponseCompleted completes the transaction even if the validation
	// URL cannot be reached (fail-open).
	ResponseCompleted ResponseType = "Completed"
	// ResponseCancelled cancels the transaction when the validation URL
	// cannot be reached (fail-closed).
	ResponseCancelled ResponseType = "Cancelled"
)

// RegisterURLRequest registers your C2B validation and confirmation URLs.
type RegisterURLRequest struct {
	ResponseType    ResponseType
	ConfirmationURL string
	ValidationURL   string
}

type registerURLsBody struct {
	ShortCode       string       `json:"ShortCode"`
	ResponseType    ResponseType `json:"ResponseType"`
	ConfirmationURL string       `json:"ConfirmationURL"`
	ValidationURL   string       `json:"ValidationURL"`
}

// RegisterC2BURLs registers C2B URLs for Config.Shortcode (Daraja v2).
// Daraja rejects URLs containing words like "mpesa" or "safaricom".
// Registering the same URLs again is safe, so this call is retried
// automatically on transient errors (see Config.MaxRetries).
func (c *Client) RegisterC2BURLs(ctx context.Context, r RegisterURLRequest) error {
	if c.cfg.Shortcode == "" {
		return errors.New("mpesa: Shortcode is required")
	}
	if r.ResponseType != ResponseCompleted && r.ResponseType != ResponseCancelled {
		return errors.New(`mpesa: ResponseType must be ResponseCompleted or ResponseCancelled`)
	}
	if r.ConfirmationURL == "" || r.ValidationURL == "" {
		return errors.New("mpesa: ConfirmationURL and ValidationURL are required")
	}
	body := registerURLsBody{
		ShortCode:       c.cfg.Shortcode,
		ResponseType:    r.ResponseType,
		ConfirmationURL: r.ConfirmationURL,
		ValidationURL:   r.ValidationURL,
	}
	var resp map[string]any
	return c.postJSON(ctx, "/mpesa/c2b/v2/registerurl", body, &resp, true)
}

// C2BNotification is the payload M-Pesa posts to your validation and
// confirmation URLs.
type C2BNotification struct {
	TransactionType   string `json:"TransactionType"`
	TransID           string `json:"TransID"`
	TransTime         string `json:"TransTime"`
	TransAmount       string `json:"TransAmount"`
	BusinessShortCode string `json:"BusinessShortCode"`
	BillRefNumber     string `json:"BillRefNumber"`
	InvoiceNumber     string `json:"InvoiceNumber"`
	OrgAccountBalance string `json:"OrgAccountBalance"`
	ThirdPartyTransID string `json:"ThirdPartyTransID"`
	MSISDN            string `json:"MSISDN"`
	FirstName         string `json:"FirstName"`
	MiddleName        string `json:"MiddleName"`
	LastName          string `json:"LastName"`
}

// Amount parses TransAmount (e.g. "500.00") as whole KES.
func (n *C2BNotification) Amount() (int64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(n.TransAmount), 64)
	if err != nil || f < 0 {
		return 0, errors.New("mpesa: unparseable TransAmount " + strconv.Quote(n.TransAmount))
	}
	return int64(f), nil
}

// ParseC2BNotification decodes a C2B validation/confirmation body.
func ParseC2BNotification(r io.Reader) (*C2BNotification, error) {
	var n C2BNotification
	if err := json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&n); err != nil {
		return nil, err
	}
	if n.TransID == "" {
		return nil, errors.New("mpesa: c2b notification missing TransID")
	}
	return &n, nil
}

// c2bResultCodes are the reply codes for validation responses. These are the
// documented Daraja values; see the portal docs for the full list.
const (
	c2bAccept     = "0"
	c2bReject     = "C2B00012" // invalid account / rejected
	c2bAcceptDesc = "Accepted"
)

func writeC2BResult(w http.ResponseWriter, code, desc, transID string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"ResultCode":        code,
		"ResultDesc":        desc,
		"ThirdPartyTransID": transID,
	})
}

// C2BValidationHandler returns an http.Handler for your C2B validation URL.
// Before money leaves the customer, Safaricom asks your fn whether to accept
// the payment: return accept=true to proceed, or accept=false with a short
// reason to reject it (e.g. unknown BillRefNumber). fn must be fast — M-Pesa
// times validation out after a few seconds.
func C2BValidationHandler(fn func(ctx context.Context, n *C2BNotification) (accept bool, reason string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		n, err := ParseC2BNotification(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		accept, reason := fn(r.Context(), n)
		if accept {
			writeC2BResult(w, c2bAccept, c2bAcceptDesc, n.TransID)
			return
		}
		if reason == "" {
			reason = "Rejected"
		}
		writeC2BResult(w, c2bReject, reason, n.TransID)
	})
}

// C2BConfirmationHandler returns an http.Handler for your C2B confirmation
// URL. The payment has already happened; fn records it. As with STK
// callbacks, fn must be idempotent — dedupe on TransID.
func C2BConfirmationHandler(fn func(ctx context.Context, n *C2BNotification) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		n, err := ParseC2BNotification(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := fn(r.Context(), n); err != nil {
			http.Error(w, "processing error", http.StatusInternalServerError)
			return
		}
		writeC2BResult(w, c2bAccept, c2bAcceptDesc, n.TransID)
	})
}
