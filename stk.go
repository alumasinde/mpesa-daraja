package mpesa

import (
	"context"
	"errors"
	"fmt"
)

// TransactionType is the STK transaction type.
type TransactionType string

const (
	// PayBill is for paybill shortcodes (default).
	PayBill TransactionType = "CustomerPayBillOnline"
	// BuyGoods is for till numbers. Set STKPushRequest.PartyB to the till.
	BuyGoods TransactionType = "CustomerBuyGoodsOnline"
)

// STKPushRequest describes a payment prompt to send to a customer's phone.
type STKPushRequest struct {
	Phone            string // any common Kenyan format; normalized for you
	Amount           int    // whole KES, >= 1
	AccountReference string // max 12 chars (paybill account / order ref)
	Description      string // max 13 chars
	CallbackURL      string // optional; falls back to Config.CallbackURL
	TransactionType  TransactionType
	PartyB           string // optional; defaults to Config.Shortcode
}

// STKPushResponse is Daraja's acknowledgement. Success here only means the
// prompt was sent; the payment result arrives on the callback.
type STKPushResponse struct {
	MerchantRequestID   string `json:"MerchantRequestID"`
	CheckoutRequestID   string `json:"CheckoutRequestID"`
	ResponseCode        string `json:"ResponseCode"`
	ResponseDescription string `json:"ResponseDescription"`
	CustomerMessage     string `json:"CustomerMessage"`
}

type stkPushBody struct {
	BusinessShortCode string `json:"BusinessShortCode"`
	Password          string `json:"Password"`
	Timestamp         string `json:"Timestamp"`
	TransactionType   string `json:"TransactionType"`
	Amount            int    `json:"Amount"`
	PartyA            string `json:"PartyA"`
	PartyB            string `json:"PartyB"`
	PhoneNumber       string `json:"PhoneNumber"`
	CallBackURL       string `json:"CallBackURL"`
	AccountReference  string `json:"AccountReference"`
	TransactionDesc   string `json:"TransactionDesc"`
}

func (c *Client) requireSTKConfig() error {
	if c.cfg.Shortcode == "" || c.cfg.Passkey == "" {
		return errors.New("mpesa: Shortcode and Passkey are required for STK operations")
	}
	return nil
}

// STKPush sends an M-Pesa payment prompt to the customer's phone.
func (c *Client) STKPush(ctx context.Context, r STKPushRequest) (*STKPushResponse, error) {
	if err := c.requireSTKConfig(); err != nil {
		return nil, err
	}
	phone, err := NormalizeMSISDN(r.Phone)
	if err != nil {
		return nil, err
	}
	if r.Amount < 1 {
		return nil, errors.New("mpesa: amount must be at least 1")
	}
	if r.AccountReference == "" || len(r.AccountReference) > 12 {
		return nil, errors.New("mpesa: AccountReference must be 1-12 characters")
	}
	if r.Description == "" || len(r.Description) > 13 {
		return nil, errors.New("mpesa: Description must be 1-13 characters")
	}
	cb := r.CallbackURL
	if cb == "" {
		cb = c.cfg.CallbackURL
	}
	if cb == "" {
		return nil, errors.New("mpesa: CallbackURL is required (request or Config)")
	}
	tt := r.TransactionType
	if tt == "" {
		tt = PayBill
	}
	partyB := r.PartyB
	if partyB == "" {
		partyB = c.cfg.Shortcode
	}

	ts := c.timestamp()
	body := stkPushBody{
		BusinessShortCode: c.cfg.Shortcode,
		Password:          c.password(ts),
		Timestamp:         ts,
		TransactionType:   string(tt),
		Amount:            r.Amount,
		PartyA:            phone,
		PartyB:            partyB,
		PhoneNumber:       phone,
		CallBackURL:       cb,
		AccountReference:  r.AccountReference,
		TransactionDesc:   r.Description,
	}

	var resp STKPushResponse
	if err := c.postJSON(ctx, "/mpesa/stkpush/v1/processrequest", body, &resp); err != nil {
		return nil, err
	}
	if resp.ResponseCode != "0" {
		return nil, fmt.Errorf("mpesa: stk push rejected: %s (%s)", resp.ResponseDescription, resp.ResponseCode)
	}
	return &resp, nil
}

// STKQueryResponse is the status of a previously initiated STK push.
type STKQueryResponse struct {
	MerchantRequestID   string `json:"MerchantRequestID"`
	CheckoutRequestID   string `json:"CheckoutRequestID"`
	ResponseCode        string `json:"ResponseCode"`
	ResponseDescription string `json:"ResponseDescription"`
	ResultCode          string `json:"ResultCode"`
	ResultDesc          string `json:"ResultDesc"`
}

// Paid reports whether the customer completed the payment.
func (q *STKQueryResponse) Paid() bool { return q.ResultCode == "0" }

// STKQuery checks the outcome of an STK push. While the customer is still
// deciding Daraja returns an error; test it with IsProcessing and retry.
func (c *Client) STKQuery(ctx context.Context, checkoutRequestID string) (*STKQueryResponse, error) {
	if err := c.requireSTKConfig(); err != nil {
		return nil, err
	}
	if checkoutRequestID == "" {
		return nil, errors.New("mpesa: checkoutRequestID is required")
	}
	ts := c.timestamp()
	body := map[string]string{
		"BusinessShortCode": c.cfg.Shortcode,
		"Password":          c.password(ts),
		"Timestamp":         ts,
		"CheckoutRequestID": checkoutRequestID,
	}
	var resp STKQueryResponse
	if err := c.postJSON(ctx, "/mpesa/stkpushquery/v1/query", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
