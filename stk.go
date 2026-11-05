package mpesa

import (
	"context"
	"errors"
	"strconv"
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

type stkQueryBody struct {
	BusinessShortCode string `json:"BusinessShortCode"`
	Password          string `json:"Password"`
	Timestamp         string `json:"Timestamp"`
	CheckoutRequestID string `json:"CheckoutRequestID"`
}

func (c *Client) requireSTKConfig() error {
	if c.cfg.Shortcode == "" || c.cfg.Passkey == "" {
		return errors.New("mpesa: Shortcode and Passkey are required for STK operations")
	}
	return nil
}

// STKPush sends an M-Pesa payment prompt to the customer's phone.
//
// STKPush is never retried automatically (a repeated push could prompt the
// customer twice). If Daraja acknowledges the request but rejects it, the
// error is a *RequestError carrying the response codes and request IDs.
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
	if err := c.postJSON(ctx, "/mpesa/stkpush/v1/processrequest", body, &resp, false); err != nil {
		return nil, err
	}
	if resp.ResponseCode != "0" {
		return nil, &RequestError{
			ResponseCode:        resp.ResponseCode,
			ResponseDescription: resp.ResponseDescription,
			MerchantRequestID:   resp.MerchantRequestID,
			CheckoutRequestID:   resp.CheckoutRequestID,
		}
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

// ResultCodeInt is the numeric ResultCode, or -1 if Daraja did not send one
// (e.g. while the transaction is still processing). Compare against the
// Result* constants.
func (q *STKQueryResponse) ResultCodeInt() int {
	n, err := parseInt(q.ResultCode)
	if err != nil {
		return -1
	}
	return n
}

// STKQuery checks the outcome of an STK push. While the customer is still
// deciding Daraja returns an error; test it with IsProcessing and retry.
// STKQuery is idempotent and is retried automatically on network errors,
// 429s and 5xxs (see Config.MaxRetries).
func (c *Client) STKQuery(ctx context.Context, checkoutRequestID string) (*STKQueryResponse, error) {
	if err := c.requireSTKConfig(); err != nil {
		return nil, err
	}
	if checkoutRequestID == "" {
		return nil, errors.New("mpesa: checkoutRequestID is required")
	}
	ts := c.timestamp()
	body := stkQueryBody{
		BusinessShortCode: c.cfg.Shortcode,
		Password:          c.password(ts),
		Timestamp:         ts,
		CheckoutRequestID: checkoutRequestID,
	}
	var resp STKQueryResponse
	if err := c.postJSON(ctx, "/mpesa/stkpushquery/v1/query", body, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

func parseInt(s string) (int, error) { return strconv.Atoi(s) }
