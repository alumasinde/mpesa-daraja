package mpesa

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
)

// CommandID values for the initiator APIs.
const (
	// B2C payment types.
	CommandBusinessPayment  = "BusinessPayment"  // default B2C; settles in ~a day
	CommandSalaryPayment    = "SalaryPayment"    // settles quickly, for payouts
	CommandPromotionPayment = "PromotionPayment" // marketing disbursements
	// Queries and reversals.
	CommandTransactionStatus = "TransactionStatusQuery"
	CommandAccountBalance    = "AccountBalanceQuery"
	CommandReversal          = "TransactionReversal"
)

// IdentifierType values: what kind of party an identifier refers to.
const (
	IdentifierMSISDN       = "1" // phone number
	IdentifierTill         = "2" // till number
	IdentifierOrgShortCode = "4" // organization paybill / shortcode
)

// EncryptSecurityCredential encrypts the initiator password with the
// Safaricom public certificate for the target environment (downloadable from
// the Daraja portal) using RSA PKCS#1 v1.5, returning the base64
// SecurityCredential the initiator APIs expect. The certificate rotates
// occasionally — if calls start failing with an authentication error, fetch
// the current cert.
func EncryptSecurityCredential(password string, certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("mpesa: no PEM block in certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("mpesa: certificate public key is not RSA")
	}
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(password))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

// AsyncResponse is Daraja's acknowledgement for the initiator APIs. It only
// means the request was queued; the actual result is POSTed to your ResultURL
// (parse it with ParseResultCallback).
type AsyncResponse struct {
	ConversationID           string `json:"ConversationID"`
	OriginatorConversationID string `json:"OriginatorConversationID"`
	ResponseCode             string `json:"ResponseCode"`
	ResponseDescription      string `json:"ResponseDescription"`
}

func checkAsync(resp *AsyncResponse) (*AsyncResponse, error) {
	if resp.ResponseCode != "0" {
		return nil, &RequestError{
			ResponseCode:        resp.ResponseCode,
			ResponseDescription: resp.ResponseDescription,
		}
	}
	return resp, nil
}

// resultURLs resolves the ResultURL / QueueTimeoutURL pair for a request,
// falling back to the Config defaults.
func (c *Client) resultURLs(resultURL, timeoutURL string) (string, string, error) {
	resultURL = orDefault(resultURL, c.cfg.ResultURL)
	timeoutURL = orDefault(timeoutURL, c.cfg.QueueTimeoutURL)
	if resultURL == "" || timeoutURL == "" {
		return "", "", errors.New("mpesa: ResultURL and QueueTimeoutURL are required (request or Config)")
	}
	return resultURL, timeoutURL, nil
}

// B2CRequest describes a payment from your organization to a customer.
type B2CRequest struct {
	// CommandID selects the payment type; defaults to CommandBusinessPayment.
	CommandID string
	Amount    int64  // whole KES, >= 1
	PartyB    string // customer phone, any common Kenyan format
	Remarks   string // required, e.g. "salary March"
	Occasion  string // optional

	ResultURL       string // optional; falls back to Config.ResultURL
	QueueTimeoutURL string // optional; falls back to Config.QueueTimeoutURL
}

type b2cBody struct {
	InitiatorName       string `json:"InitiatorName"`
	SecurityCredential  string `json:"SecurityCredential"`
	CommandID           string `json:"CommandID"`
	Amount              int64  `json:"Amount"`
	PartyA              string `json:"PartyA"`
	PartyB              string `json:"PartyB"`
	Remarks             string `json:"Remarks"`
	QueueTimeOutURL     string `json:"QueueTimeOutURL"`
	ResultURL           string `json:"ResultURL"`
	Occasion            string `json:"Occasion"`
}

// B2C sends money from Config.Shortcode to a customer. B2C is never retried
// automatically — a repeat could pay twice. The definitive result arrives on
// your ResultURL; see ParseResultCallback.
func (c *Client) B2C(ctx context.Context, r B2CRequest) (*AsyncResponse, error) {
	cred, err := c.requireInitiator()
	if err != nil {
		return nil, err
	}
	if r.Amount < 1 {
		return nil, errors.New("mpesa: amount must be at least 1")
	}
	phone, err := NormalizeMSISDN(r.PartyB)
	if err != nil {
		return nil, err
	}
	if r.Remarks == "" {
		return nil, errors.New("mpesa: Remarks are required")
	}
	resultURL, timeoutURL, err := c.resultURLs(r.ResultURL, r.QueueTimeoutURL)
	if err != nil {
		return nil, err
	}
	body := b2cBody{
		InitiatorName:      c.cfg.InitiatorName,
		SecurityCredential: cred,
		CommandID:          orDefault(r.CommandID, CommandBusinessPayment),
		Amount:             r.Amount,
		PartyA:             c.cfg.Shortcode,
		PartyB:             phone,
		Remarks:            r.Remarks,
		QueueTimeOutURL:    timeoutURL,
		ResultURL:          resultURL,
		Occasion:           r.Occasion,
	}
	var resp AsyncResponse
	if err := c.postJSON(ctx, "/mpesa/b2c/v3/paymentrequest", body, &resp, false); err != nil {
		return nil, err
	}
	return checkAsync(&resp)
}

// TransactionStatusRequest queries the state of a previous transaction by
// its M-Pesa receipt (e.g. "NLJ7RT61SV"), from any API.
type TransactionStatusRequest struct {
	TransactionID string // M-Pesa receipt to look up (required)

	// IdentifierType describes PartyA — the account paying for the query —
	// not the transaction. Defaults to IdentifierOrgShortCode with
	// Config.Shortcode; override both together per the Daraja docs.
	IdentifierType string
	PartyA         string

	Remarks  string // required
	Occasion string // optional

	ResultURL       string
	QueueTimeoutURL string
}

type transactionStatusBody struct {
	InitiatorName      string `json:"InitiatorName"`
	SecurityCredential string `json:"SecurityCredential"`
	CommandID          string `json:"CommandID"`
	TransactionID      string `json:"TransactionID"`
	PartyA             string `json:"PartyA"`
	IdentifierType     string `json:"IdentifierType"`
	ResultURL          string `json:"ResultURL"`
	QueueTimeOutURL    string `json:"QueueTimeOutURL"`
	Remarks            string `json:"Remarks"`
	Occasion           string `json:"Occasion"`
}

// TransactionStatus asks Daraja to POST a full status report for a
// transaction to your ResultURL; see ParseResultCallback.
func (c *Client) TransactionStatus(ctx context.Context, r TransactionStatusRequest) (*AsyncResponse, error) {
	cred, err := c.requireInitiator()
	if err != nil {
		return nil, err
	}
	if r.TransactionID == "" {
		return nil, errors.New("mpesa: TransactionID is required")
	}
	if r.Remarks == "" {
		return nil, errors.New("mpesa: Remarks are required")
	}
	resultURL, timeoutURL, err := c.resultURLs(r.ResultURL, r.QueueTimeoutURL)
	if err != nil {
		return nil, err
	}
	body := transactionStatusBody{
		InitiatorName:      c.cfg.InitiatorName,
		SecurityCredential: cred,
		CommandID:          CommandTransactionStatus,
		TransactionID:      r.TransactionID,
		PartyA:             orDefault(r.PartyA, c.cfg.Shortcode),
		IdentifierType:     orDefault(r.IdentifierType, IdentifierOrgShortCode),
		ResultURL:          resultURL,
		QueueTimeOutURL:    timeoutURL,
		Remarks:            r.Remarks,
		Occasion:           r.Occasion,
	}
	var resp AsyncResponse
	if err := c.postJSON(ctx, "/mpesa/transactionstatus/v1/query", body, &resp, true); err != nil {
		return nil, err
	}
	return checkAsync(&resp)
}

// AccountBalanceRequest asks for the balance of one of your M-Pesa accounts.
type AccountBalanceRequest struct {
	// IdentifierType + Party select the account; they default to your
	// organization shortcode. For a till, set Party to the till number and
	// IdentifierType to IdentifierTill.
	IdentifierType string
	Party          string

	Remarks  string // required
	Occasion string // optional

	ResultURL       string
	QueueTimeoutURL string
}

type accountBalanceBody struct {
	InitiatorName      string `json:"InitiatorName"`
	SecurityCredential string `json:"SecurityCredential"`
	CommandID          string `json:"CommandID"`
	PartyA             string `json:"PartyA"`
	IdentifierType     string `json:"IdentifierType"`
	Remarks            string `json:"Remarks"`
	QueueTimeOutURL    string `json:"QueueTimeOutURL"`
	ResultURL          string `json:"ResultURL"`
}

// AccountBalance asks Daraja to POST the account balance to your ResultURL;
// see ParseResultCallback.
func (c *Client) AccountBalance(ctx context.Context, r AccountBalanceRequest) (*AsyncResponse, error) {
	cred, err := c.requireInitiator()
	if err != nil {
		return nil, err
	}
	if r.Remarks == "" {
		return nil, errors.New("mpesa: Remarks are required")
	}
	resultURL, timeoutURL, err := c.resultURLs(r.ResultURL, r.QueueTimeoutURL)
	if err != nil {
		return nil, err
	}
	body := accountBalanceBody{
		InitiatorName:      c.cfg.InitiatorName,
		SecurityCredential: cred,
		CommandID:          CommandAccountBalance,
		PartyA:             orDefault(r.Party, c.cfg.Shortcode),
		IdentifierType:     orDefault(r.IdentifierType, IdentifierOrgShortCode),
		Remarks:            r.Remarks,
		QueueTimeOutURL:    timeoutURL,
		ResultURL:          resultURL,
	}
	var resp AsyncResponse
	if err := c.postJSON(ctx, "/mpesa/accountbalance/v1/query", body, &resp, true); err != nil {
		return nil, err
	}
	return checkAsync(&resp)
}

// ReversalRequest reverses a previous C2B payment by receipt number.
// Reversals require go-ahead from Safaricom (they are not enabled by
// default) and are never retried automatically.
type ReversalRequest struct {
	TransactionID string // receipt of the payment to reverse (required)
	Amount        int64  // whole KES, >= 1
	ReceiverParty string // optional; defaults to Config.Shortcode
	Remarks       string // required
	Occasion      string // optional

	ResultURL       string
	QueueTimeoutURL string
}

type reversalBody struct {
	InitiatorName           string `json:"InitiatorName"`
	SecurityCredential      string `json:"SecurityCredential"`
	CommandID               string `json:"CommandID"`
	TransactionID           string `json:"TransactionID"`
	Amount                  int64  `json:"Amount"`
	ReceiverParty           string `json:"ReceiverParty"`
	ReceiverIdentifierType  string `json:"ReceiverIdentifierType"`
	ResultURL               string `json:"ResultURL"`
	QueueTimeOutURL         string `json:"QueueTimeOutURL"`
	Remarks                 string `json:"Remarks"`
	Occasion                string `json:"Occasion"`
}

// Reversal requests the reversal of a transaction. The outcome is POSTed to
// your ResultURL; see ParseResultCallback.
func (c *Client) Reversal(ctx context.Context, r ReversalRequest) (*AsyncResponse, error) {
	cred, err := c.requireInitiator()
	if err != nil {
		return nil, err
	}
	if r.TransactionID == "" {
		return nil, errors.New("mpesa: TransactionID is required")
	}
	if r.Amount < 1 {
		return nil, errors.New("mpesa: amount must be at least 1")
	}
	if r.Remarks == "" {
		return nil, errors.New("mpesa: Remarks are required")
	}
	resultURL, timeoutURL, err := c.resultURLs(r.ResultURL, r.QueueTimeoutURL)
	if err != nil {
		return nil, err
	}
	body := reversalBody{
		InitiatorName:          c.cfg.InitiatorName,
		SecurityCredential:     cred,
		CommandID:              CommandReversal,
		TransactionID:          r.TransactionID,
		Amount:                 r.Amount,
		ReceiverParty:          orDefault(r.ReceiverParty, c.cfg.Shortcode),
		ReceiverIdentifierType: IdentifierOrgShortCode,
		ResultURL:              resultURL,
		QueueTimeOutURL:        timeoutURL,
		Remarks:                r.Remarks,
		Occasion:               r.Occasion,
	}
	var resp AsyncResponse
	if err := c.postJSON(ctx, "/mpesa/reversal/v1/request", body, &resp, false); err != nil {
		return nil, err
	}
	return checkAsync(&resp)
}
