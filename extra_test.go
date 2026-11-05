package mpesa

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tokenAndHandler builds a mux that always issues a token, then delegates
// other paths to h (nil: 404).
func tokenMux(t *testing.T, h http.HandlerFunc) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t","expires_in":"3599"}`))
	})
	if h != nil {
		mux.HandleFunc("/", h)
	}
	return mux
}

func TestUserAgentSent(t *testing.T) {
	var gotUA, gotTokUA string
	mux := tokenMux(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"ResponseCode":"0"}`))
	})
	mux.HandleFunc("/oauth/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		gotTokUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"access_token":"t","expires_in":"3599"}`))
	})
	c := newTestClient(t, mux)
	_, _ = c.STKQuery(context.Background(), "c1")
	want := "mpesa-daraja-go/" + Version
	if gotUA != want || gotTokUA != want {
		t.Errorf("user-agent: api=%q token=%q want %q", gotUA, gotTokUA, want)
	}
}

func TestRequestErrorOnSTKRejection(t *testing.T) {
	mux := tokenMux(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"MerchantRequestID":"m9","CheckoutRequestID":"c9","ResponseCode":"1","ResponseDescription":"Service is busy"}`))
	})
	c := newTestClient(t, mux)
	_, err := c.STKPush(context.Background(), STKPushRequest{
		Phone: "0712345678", Amount: 10, AccountReference: "A", Description: "D",
	})
	re, ok := err.(*RequestError)
	if !ok {
		t.Fatalf("want *RequestError, got %T %v", err, err)
	}
	if re.ResponseCode != "1" || re.ResponseDescription != "Service is busy" ||
		re.MerchantRequestID != "m9" || re.CheckoutRequestID != "c9" {
		t.Errorf("bad RequestError: %+v", re)
	}
	if !IsRejected(err) || IsRejected(nil) {
		t.Error("IsRejected misbehaving")
	}
}

func TestRetriesIdempotentQueries(t *testing.T) {
	var calls int32
	mux := tokenMux(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(503) // empty body: looks like a proxy failure
			return
		}
		_, _ = w.Write([]byte(`{"ResponseCode":"0","ResultCode":"0","ResultDesc":"done"}`))
	})
	c := newTestClient(t, mux)
	start := time.Now()
	q, err := c.STKQuery(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !q.Paid() || atomic.LoadInt32(&calls) != 2 {
		t.Errorf("paid=%v calls=%d", q.Paid(), calls)
	}
	if d := time.Since(start); d < 400*time.Millisecond {
		t.Errorf("retried too fast (%v); expected backoff", d)
	}
}

func TestNoRetryForSTKPush(t *testing.T) {
	var calls int32
	mux := tokenMux(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(502)
	})
	c := newTestClient(t, mux)
	if _, err := c.STKPush(context.Background(), STKPushRequest{
		Phone: "0712345678", Amount: 10, AccountReference: "A", Description: "D",
	}); err == nil {
		t.Fatal("expected error")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("STKPush attempted %d times; money-moving calls must not retry", n)
	}
}

func TestDarajaErrorBodyNotRetried(t *testing.T) {
	var calls int32
	mux := tokenMux(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"requestId":"r","errorCode":"500.001.1001","errorMessage":"The transaction is being processed"}`))
	})
	c := newTestClient(t, mux)
	_, err := c.STKQuery(context.Background(), "c1")
	if !IsProcessing(err) {
		t.Fatalf("want processing error, got %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("application-level 5xx retried %d times", n)
	}
}

func TestConfigRedaction(t *testing.T) {
	cfg := Config{
		ConsumerKey: "ck-secret", ConsumerSecret: "cs-secret",
		Shortcode: "174379", Passkey: "pk-secret",
		Env: Sandbox, InitiatorPassword: "ip-secret", SecurityCredential: "sc-secret",
	}
	for _, s := range []string{cfg.String()} {
		for _, secret := range []string{"ck-secret", "cs-secret", "pk-secret", "ip-secret", "sc-secret"} {
			if strings.Contains(s, secret) {
				t.Errorf("String() leaks %q: %s", secret, s)
			}
		}
		if !strings.Contains(s, "****") || !strings.Contains(s, "174379") {
			t.Errorf("String() should keep shape but redact: %s", s)
		}
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.String(), "ck-secret") {
		t.Errorf("Client.String() leaks: %s", c.String())
	}
}

func TestSTKQueryResultCodeInt(t *testing.T) {
	mux := tokenMux(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ResponseCode":"0","ResultCode":"1032","ResultDesc":"Request cancelled by user"}`))
	})
	c := newTestClient(t, mux)
	q, err := c.STKQuery(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if q.Paid() || q.ResultCodeInt() != ResultCancelledByUser {
		t.Errorf("ResultCodeInt=%d", q.ResultCodeInt())
	}
}

const c2bNotification = `{"TransactionType":"Pay Bill","TransID":"RKTQH7JXVZ","TransTime":"20261101093015","TransAmount":"500.00","BusinessShortCode":"174379","BillRefNumber":"INV-1","InvoiceNumber":"","OrgAccountBalance":"1000.00","ThirdPartyTransID":"","MSISDN":"254712345678","FirstName":"JOHN","MiddleName":"","LastName":"DOE"}`

func TestC2BAmountHelper(t *testing.T) {
	n, err := ParseC2BNotification(strings.NewReader(c2bNotification))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := n.Amount(); err != nil || a != 500 {
		t.Errorf("Amount()=%d %v", a, err)
	}
}

func TestC2BValidationHandler(t *testing.T) {
	var got *C2BNotification
	h := C2BValidationHandler(func(ctx context.Context, n *C2BNotification) (bool, string) {
		got = n
		return n.BillRefNumber == "INV-1", ""
	})

	rec := httptestRecorder(h, "POST", c2bNotification)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ResultCode":"0"`) {
		t.Errorf("accept: code=%d body=%s", rec.Code, rec.Body)
	}
	if got == nil || got.TransID != "RKTQH7JXVZ" {
		t.Errorf("notification not delivered: %+v", got)
	}

	// Unknown bill reference -> reject with C2B00012.
	h = C2BValidationHandler(func(ctx context.Context, n *C2BNotification) (bool, string) {
		return false, "Unknown account"
	})
	rec = httptestRecorder(h, "POST", strings.Replace(c2bNotification, "INV-1", "NOPE", 1))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "C2B00012") ||
		!strings.Contains(rec.Body.String(), "Unknown account") {
		t.Errorf("reject: code=%d body=%s", rec.Code, rec.Body)
	}
	if rec := httptestRecorder(h, "GET", ""); rec.Code != 405 {
		t.Errorf("GET code=%d", rec.Code)
	}
}

func TestC2BConfirmationHandler(t *testing.T) {
	called := 0
	h := C2BConfirmationHandler(func(ctx context.Context, n *C2BNotification) error {
		called++
		return nil
	})
	rec := httptestRecorder(h, "POST", c2bNotification)
	if rec.Code != 200 || called != 1 || !strings.Contains(rec.Body.String(), `"ResultCode":"0"`) {
		t.Errorf("code=%d called=%d body=%s", rec.Code, called, rec.Body)
	}
	if rec := httptestRecorder(h, "POST", "garbage"); rec.Code != 400 {
		t.Errorf("garbage code=%d", rec.Code)
	}
}

func TestEncryptSecurityCredential(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "daraja-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	cred, err := EncryptSecurityCredential("initiator-pass", certPEM)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(cred)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, key, raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "initiator-pass" {
		t.Errorf("round-trip mismatch: %q", plain)
	}
	if _, err := EncryptSecurityCredential("x", []byte("not pem")); err == nil {
		t.Error("expected error for bad PEM")
	}
}

func newInitiatorTestClient(t *testing.T, path string, check func(t *testing.T, body map[string]any)) *Client {
	t.Helper()
	mux := tokenMux(t, nil)
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		check(t, body)
		_, _ = w.Write([]byte(`{"ConversationID":"AG_2376487236_126732989","OriginatorConversationID":"org-1","ResponseCode":"0","ResponseDescription":"Accept the service request successfully."}`))
	})
	c, err := New(Config{
		ConsumerKey: "key", ConsumerSecret: "secret",
		Shortcode: "600426", Passkey: "passkey",
		InitiatorName: "testop", SecurityCredential: "cred-1",
		ResultURL: "https://x.test/result", QueueTimeoutURL: "https://x.test/timeout",
		BaseURL: mustServerURL(t, mux), Env: Sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustServerURL(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := &http.Server{Handler: h}
	ln, err := netListen()
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

func TestB2C(t *testing.T) {
	c := newInitiatorTestClient(t, "/mpesa/b2c/v3/paymentrequest", func(t *testing.T, b map[string]any) {
		if b["InitiatorName"] != "testop" || b["SecurityCredential"] != "cred-1" ||
			b["CommandID"] != "SalaryPayment" || b["Amount"] != float64(1500) ||
			b["PartyA"] != "600426" || b["PartyB"] != "254712345678" ||
			b["Remarks"] != "payout" || b["ResultURL"] != "https://x.test/result" {
			t.Errorf("bad b2c body: %v", b)
		}
	})
	resp, err := c.B2C(context.Background(), B2CRequest{
		CommandID: CommandSalaryPayment, Amount: 1500, PartyB: "0712345678", Remarks: "payout",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ConversationID == "" || resp.OriginatorConversationID != "org-1" {
		t.Errorf("bad ack: %+v", resp)
	}

	if _, err := c.B2C(context.Background(), B2CRequest{Amount: 5, PartyB: "0712345678"}); err == nil {
		t.Error("expected validation error (missing Remarks)")
	}
	noInitiator, err := New(Config{ConsumerKey: "k", ConsumerSecret: "s", Shortcode: "1", Passkey: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noInitiator.B2C(context.Background(), B2CRequest{Amount: 5, PartyB: "0712345678", Remarks: "r"}); err == nil {
		t.Error("expected error for missing initiator config")
	}
}

func TestTransactionStatusRequest(t *testing.T) {
	c := newInitiatorTestClient(t, "/mpesa/transactionstatus/v1/query", func(t *testing.T, b map[string]any) {
		if b["CommandID"] != CommandTransactionStatus || b["TransactionID"] != "NLJ7RT61SV" ||
			b["IdentifierType"] != IdentifierOrgShortCode || b["PartyA"] != "600426" ||
			b["Remarks"] != "check" {
			t.Errorf("bad status body: %v", b)
		}
	})
	if _, err := c.TransactionStatus(context.Background(), TransactionStatusRequest{
		TransactionID: "NLJ7RT61SV", Remarks: "check",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TransactionStatus(context.Background(), TransactionStatusRequest{Remarks: "r"}); err == nil {
		t.Error("expected validation error (missing TransactionID)")
	}
}

func TestAccountBalanceRequest(t *testing.T) {
	c := newInitiatorTestClient(t, "/mpesa/accountbalance/v1/query", func(t *testing.T, b map[string]any) {
		if b["CommandID"] != CommandAccountBalance || b["PartyA"] != "600426" ||
			b["IdentifierType"] != IdentifierOrgShortCode || b["Remarks"] != "balance" {
			t.Errorf("bad balance body: %v", b)
		}
	})
	if _, err := c.AccountBalance(context.Background(), AccountBalanceRequest{Remarks: "balance"}); err != nil {
		t.Fatal(err)
	}
}

func TestReversalRequest(t *testing.T) {
	c := newInitiatorTestClient(t, "/mpesa/reversal/v1/request", func(t *testing.T, b map[string]any) {
		if b["CommandID"] != CommandReversal || b["TransactionID"] != "NLJ7RT61SV" ||
			b["Amount"] != float64(500) || b["ReceiverParty"] != "600426" ||
			b["ReceiverIdentifierType"] != IdentifierOrgShortCode {
			t.Errorf("bad reversal body: %v", b)
		}
	})
	if _, err := c.Reversal(context.Background(), ReversalRequest{
		TransactionID: "NLJ7RT61SV", Amount: 500, Remarks: "wrong number",
	}); err != nil {
		t.Fatal(err)
	}
}

const resultCallback = `{"Result":{"ResultType":0,"ResultCode":0,"ResultDesc":"The service request is processed successfully.","OriginatorConversationID":"org-1","ConversationID":"AG_1","TransactionID":"NLJ7RT61SV","ResultParameters":{"ResultParameter":[{"Key":"TransactionAmount","Value":10},{"Key":"TransactionReceipt","Value":"NLJ7RT61SV"},{"Key":"B2CChargesPaidAccountAvailableFunds":"100.00"}]},"ReferenceData":{"ReferenceItem":[{"Key":"QueueTimeoutURL","Value":"https://x.test/timeout"}]}}}`

func TestParseResultCallback(t *testing.T) {
	rc, err := ParseResultCallback(strings.NewReader(resultCallback))
	if err != nil {
		t.Fatal(err)
	}
	if !rc.Success() || rc.TransactionID != "NLJ7RT61SV" || rc.Parameters["TransactionAmount"] != "10" ||
		rc.Parameters["TransactionReceipt"] != "NLJ7RT61SV" {
		t.Errorf("bad parse: %+v", rc)
	}
	if _, err := ParseResultCallback(strings.NewReader(`{}`)); err == nil {
		t.Error("expected error for empty body")
	}
}

func TestResultHandler(t *testing.T) {
	var got *ResultCallback
	h := ResultHandler(func(ctx context.Context, rc *ResultCallback) error { got = rc; return nil })
	rec := httptestRecorder(h, "POST", resultCallback)
	if rec.Code != 200 || got == nil || !strings.Contains(rec.Body.String(), `"ResultCode":0`) {
		t.Errorf("code=%d body=%s got=%v", rec.Code, rec.Body, got)
	}
	if rec := httptestRecorder(h, "GET", ""); rec.Code != 405 {
		t.Errorf("GET code=%d", rec.Code)
	}
}

func TestRegisterURLsRejectsBadResponseType(t *testing.T) {
	c := newTestClient(t, tokenMux(t, nil))
	err := c.RegisterC2BURLs(context.Background(), RegisterURLRequest{
		ResponseType: ResponseCompleted, // typed constant works
		ValidationURL: "https://x.test/v", ConfirmationURL: "https://x.test/c",
	})
	_ = err // request may 404 on the fake server; validation is what matters below
	if err2 := c.RegisterC2BURLs(context.Background(), RegisterURLRequest{
		ResponseType: "sometimes", ValidationURL: "v", ConfirmationURL: "c",
	}); err2 == nil {
		t.Error("expected validation error for bad ResponseType")
	}
}
