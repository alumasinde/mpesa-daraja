package mpesa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

// RegisterURLRequest registers your C2B validation and confirmation URLs.
type RegisterURLRequest struct {
	// ResponseType is what M-Pesa does if the validation URL is unreachable:
	// "Completed" or "Cancelled".
	ResponseType    string
	ConfirmationURL string
	ValidationURL   string
}

// RegisterC2BURLs registers C2B URLs for Config.Shortcode (Daraja v2).
// Daraja rejects URLs containing words like "mpesa" or "safaricom".
func (c *Client) RegisterC2BURLs(ctx context.Context, r RegisterURLRequest) error {
	if c.cfg.Shortcode == "" {
		return errors.New("mpesa: Shortcode is required")
	}
	if r.ResponseType != "Completed" && r.ResponseType != "Cancelled" {
		return errors.New(`mpesa: ResponseType must be "Completed" or "Cancelled"`)
	}
	if r.ConfirmationURL == "" || r.ValidationURL == "" {
		return errors.New("mpesa: ConfirmationURL and ValidationURL are required")
	}
	body := map[string]string{
		"ShortCode":       c.cfg.Shortcode,
		"ResponseType":    r.ResponseType,
		"ConfirmationURL": r.ConfirmationURL,
		"ValidationURL":   r.ValidationURL,
	}
	var resp map[string]any
	return c.postJSON(ctx, "/mpesa/c2b/v2/registerurl", body, &resp)
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
