package mpesa

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{
		ConsumerKey: "key", ConsumerSecret: "secret",
		Shortcode: "174379", Passkey: "passkey",
		CallbackURL: "https://example.com/cb",
		BaseURL:     srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return time.Date(2026, 11, 1, 9, 30, 15, 0, time.UTC) }
	return c
}

func TestNormalizeMSISDN(t *testing.T) {
	ok := map[string]string{
		"0712345678":     "254712345678",
		"0112345678":     "254112345678",
		"712345678":      "254712345678",
		"+254712345678":  "254712345678",
		"254 712 345678": "254712345678",
		"0712-345-678":   "254712345678",
	}
	for in, want := range ok {
		got, err := NormalizeMSISDN(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "071234567", "0812345678", "abc", "2547123456789", "0212345678"} {
		if got, err := NormalizeMSISDN(in); err == nil {
			t.Errorf("%q: expected error, got %q", in, got)
		}
	}
}

func TestSTKPushAndTokenCaching(t *testing.T) {
	var tokenCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		u, p, _ := r.BasicAuth()
		if u != "key" || p != "secret" {
			http.Error(w, "no", 401)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok123","expires_in":"3599"}`))
	})
	mux.HandleFunc("/mpesa/stkpush/v1/processrequest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok123" {
			http.Error(w, "unauth", 401)
			return
		}
		var b stkPushBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		wantTS := "20261101123015" // 09:30:15 UTC == 12:30:15 EAT
		wantPw := base64.StdEncoding.EncodeToString([]byte("174379passkey" + wantTS))
		if b.Timestamp != wantTS || b.Password != wantPw {
			t.Errorf("bad ts/password: %s %s", b.Timestamp, b.Password)
		}
		if b.PartyA != "254712345678" || b.PhoneNumber != "254712345678" || b.PartyB != "174379" ||
			b.Amount != 10 || b.TransactionType != "CustomerPayBillOnline" || b.CallBackURL != "https://example.com/cb" {
			t.Errorf("bad body: %+v", b)
		}
		_, _ = w.Write([]byte(`{"MerchantRequestID":"m1","CheckoutRequestID":"c1","ResponseCode":"0","ResponseDescription":"ok","CustomerMessage":"Success"}`))
	})
	c := newTestClient(t, mux)

	for i := 0; i < 2; i++ {
		resp, err := c.STKPush(context.Background(), STKPushRequest{
			Phone: "0712345678", Amount: 10, AccountReference: "INV-1", Description: "Payment",
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.CheckoutRequestID != "c1" {
			t.Fatalf("got %+v", resp)
		}
	}
	if n := atomic.LoadInt32(&tokenCalls); n != 1 {
		t.Errorf("token fetched %d times, want 1", n)
	}
}

func TestRetryOn401RefreshesToken(t *testing.T) {
	var tokenCalls, stkCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&tokenCalls, 1)
		_, _ = w.Write([]byte(`{"access_token":"tok` + string(rune('0'+n)) + `","expires_in":"3599"}`))
	})
	mux.HandleFunc("/mpesa/stkpushquery/v1/query", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&stkCalls, 1)
		if r.Header.Get("Authorization") != "Bearer tok2" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"ResponseCode":"0","ResultCode":"0","ResultDesc":"done","CheckoutRequestID":"c1"}`))
	})
	c := newTestClient(t, mux)
	q, err := c.STKQuery(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !q.Paid() || tokenCalls != 2 || stkCalls != 2 {
		t.Errorf("paid=%v tokenCalls=%d stkCalls=%d", q.Paid(), tokenCalls, stkCalls)
	}
}

func TestAPIErrorAndProcessing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t","expires_in":"3599"}`))
	})
	mux.HandleFunc("/mpesa/stkpushquery/v1/query", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"requestId":"r1","errorCode":"500.001.1001","errorMessage":"The transaction is being processed"}`))
	})
	mux.HandleFunc("/mpesa/stkpush/v1/processrequest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"requestId":"r2","errorCode":"400.002.02","errorMessage":"Bad Request - Invalid PhoneNumber"}`))
	})
	c := newTestClient(t, mux)

	if _, err := c.STKQuery(context.Background(), "c1"); !IsProcessing(err) {
		t.Errorf("expected processing error, got %v", err)
	}
	_, err := c.STKPush(context.Background(), STKPushRequest{Phone: "0712345678", Amount: 1, AccountReference: "A", Description: "D"})
	ae, ok := err.(*APIError)
	if !ok || ae.Code != "400.002.02" || ae.HTTPStatus != 400 || IsProcessing(err) {
		t.Errorf("unexpected error: %#v", err)
	}
}

func TestSTKPushValidation(t *testing.T) {
	c := newTestClient(t, http.NotFoundHandler())
	cases := []STKPushRequest{
		{Phone: "bad", Amount: 1, AccountReference: "A", Description: "D"},
		{Phone: "0712345678", Amount: 0, AccountReference: "A", Description: "D"},
		{Phone: "0712345678", Amount: 1, AccountReference: "TOO-LONG-REFERENCE", Description: "D"},
		{Phone: "0712345678", Amount: 1, AccountReference: "A", Description: ""},
	}
	for i, r := range cases {
		if _, err := c.STKPush(context.Background(), r); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

const successCallback = `{"Body":{"stkCallback":{"MerchantRequestID":"m1","CheckoutRequestID":"c1","ResultCode":0,"ResultDesc":"The service request is processed successfully.","CallbackMetadata":{"Item":[{"Name":"Amount","Value":1.00},{"Name":"MpesaReceiptNumber","Value":"NLJ7RT61SV"},{"Name":"TransactionDate","Value":20191219102115},{"Name":"PhoneNumber","Value":254708374149}]}}}}`

const cancelledCallback = `{"Body":{"stkCallback":{"MerchantRequestID":"m1","CheckoutRequestID":"c1","ResultCode":1032,"ResultDesc":"Request cancelled by user"}}}`

func TestParseSTKCallback(t *testing.T) {
	cb, err := ParseSTKCallback(strings.NewReader(successCallback))
	if err != nil {
		t.Fatal(err)
	}
	if !cb.Success() || cb.Amount != 1 || cb.MpesaReceiptNumber != "NLJ7RT61SV" ||
		cb.PhoneNumber != "254708374149" || cb.CheckoutRequestID != "c1" {
		t.Errorf("bad parse: %+v", cb)
	}
	want := time.Date(2019, 12, 19, 10, 21, 15, 0, eat)
	if !cb.TransactionDate.Equal(want) {
		t.Errorf("date %v, want %v", cb.TransactionDate, want)
	}

	cb, err = ParseSTKCallback(strings.NewReader(cancelledCallback))
	if err != nil || cb.Success() || cb.ResultCode != 1032 || cb.MpesaReceiptNumber != "" {
		t.Errorf("cancelled: %+v %v", cb, err)
	}

	if _, err := ParseSTKCallback(strings.NewReader(`{}`)); err == nil {
		t.Error("expected error for empty body")
	}
}

func TestCallbackHandler(t *testing.T) {
	var got *STKCallback
	h := CallbackHandler(func(ctx context.Context, cb *STKCallback) error { got = cb; return nil })

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/cb", strings.NewReader(successCallback)))
	if rec.Code != 200 || got == nil || !strings.Contains(rec.Body.String(), `"ResultCode":0`) {
		t.Errorf("code=%d body=%s got=%v", rec.Code, rec.Body, got)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/cb", nil))
	if rec.Code != 405 {
		t.Errorf("GET code=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/cb", strings.NewReader("garbage")))
	if rec.Code != 400 {
		t.Errorf("garbage code=%d", rec.Code)
	}
}

func TestRegisterC2BURLs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t","expires_in":"3599"}`))
	})
	mux.HandleFunc("/mpesa/c2b/v2/registerurl", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["ShortCode"] != "174379" || b["ResponseType"] != "Completed" {
			t.Errorf("bad body %v", b)
		}
		_, _ = w.Write([]byte(`{"ResponseCode":"0","ResponseDescription":"Success"}`))
	})
	c := newTestClient(t, mux)
	err := c.RegisterC2BURLs(context.Background(), RegisterURLRequest{
		ResponseType: "Completed", ConfirmationURL: "https://x.test/c", ValidationURL: "https://x.test/v",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterC2BURLs(context.Background(), RegisterURLRequest{ResponseType: "bad"}); err == nil {
		t.Error("expected validation error")
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("expected error for missing credentials")
	}
	if _, err := New(Config{ConsumerKey: "a", ConsumerSecret: "b", Env: "staging"}); err == nil {
		t.Error("expected error for unknown env")
	}
}
