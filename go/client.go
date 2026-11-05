// Package mpesa is a small, dependency-free client for Safaricom's Daraja API.
//
// It covers OAuth token caching, STK Push (Lipa na M-Pesa Online), STK query,
// C2B URL registration, callback parsing, and the initiator-authenticated
// APIs: B2C payments, transaction status, account balance and reversals.
// The package uses the standard library only.
package mpesa

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Version is the library version, reported in the User-Agent header on every
// request to Daraja.
const Version = "0.2.0"

const userAgent = "mpesa-daraja-go/" + Version

// DefaultMaxRetries is the number of automatic retries applied to idempotent
// requests (STKQuery, RegisterC2BURLs, TransactionStatus, AccountBalance)
// when Config.MaxRetries is unset. Requests that move money (STKPush, B2C,
// Reversal) are never retried automatically.
const DefaultMaxRetries = 2

// Environment selects the Daraja host.
type Environment string

const (
	Sandbox    Environment = "sandbox"
	Production Environment = "production"
)

func (e Environment) baseURL() string {
	if e == Production {
		return "https://api.safaricom.co.ke"
	}
	return "https://sandbox.safaricom.co.ke"
}

// Config holds per-business credentials. Create one Client per shortcode.
// Load values from your own config/secret store; never hard-code them.
//
// Config implements fmt.Stringer with all secrets redacted, so accidentally
// logging a Config (or a Client) with %v/%+v does not leak credentials.
type Config struct {
	ConsumerKey    string
	ConsumerSecret string

	// Shortcode is the paybill / store number (BusinessShortCode).
	Shortcode string
	// Passkey is the Lipa na M-Pesa Online passkey for Shortcode.
	Passkey string
	// CallbackURL is the default STK callback URL (overridable per request).
	CallbackURL string

	// ResultURL and QueueTimeoutURL are the defaults for the initiator
	// APIs (B2C, TransactionStatus, AccountBalance, Reversal); each request
	// can override them.
	ResultURL       string
	QueueTimeoutURL string

	// InitiatorName is the API operator username for the initiator APIs.
	InitiatorName string
	// InitiatorPassword is encrypted with the Safaricom certificate to
	// produce the SecurityCredential sent on initiator API calls.
	InitiatorPassword string
	// SecurityCredential, when set, is sent as-is and overrides
	// InitiatorPassword + CertificatePEM. Use it if you encrypt the
	// credential yourself (e.g. you store it pre-encrypted).
	SecurityCredential string
	// CertificatePEM is the Safaricom public certificate for the target
	// environment, downloaded from the Daraja portal. Required to encrypt
	// InitiatorPassword when SecurityCredential is not set.
	CertificatePEM []byte

	// Env defaults to Sandbox.
	Env Environment

	// MaxRetries bounds automatic retries for idempotent requests. Zero
	// means DefaultMaxRetries; a negative value disables retries.
	MaxRetries int

	// HTTPClient is optional; defaults to a client with a 30s timeout.
	HTTPClient *http.Client
	// BaseURL overrides the Daraja host (tests, proxies).
	BaseURL string
}

// String renders the config with every secret replaced by "****". Empty
// secrets render as empty, so the shape of the config stays visible.
func (c Config) String() string {
	return fmt.Sprintf("mpesa.Config{Env:%q Shortcode:%q ConsumerKey:%s ConsumerSecret:%s "+
		"Passkey:%s CallbackURL:%q ResultURL:%q QueueTimeoutURL:%q InitiatorName:%q "+
		"InitiatorPassword:%s SecurityCredential:%s}",
		c.Env, c.Shortcode, redact(c.ConsumerKey), redact(c.ConsumerSecret),
		redact(c.Passkey), c.CallbackURL, c.ResultURL, c.QueueTimeoutURL,
		c.InitiatorName, redact(c.InitiatorPassword), redact(c.SecurityCredential))
}

func redact(s string) string {
	if s == "" {
		return ""
	}
	return "****"
}

// Client is safe for concurrent use. Its String method redacts secrets, so
// logging a Client never leaks credentials.
type Client struct {
	cfg  Config
	base string
	hc   *http.Client
	now  func() time.Time

	mu       sync.Mutex
	token    string
	tokenExp time.Time

	credMu  sync.Mutex
	cred    string
	credErr error
}

// eat is East Africa Time; Daraja timestamps must be in it.
var eat = time.FixedZone("EAT", 3*60*60)

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	if cfg.ConsumerKey == "" || cfg.ConsumerSecret == "" {
		return nil, errors.New("mpesa: ConsumerKey and ConsumerSecret are required")
	}
	if cfg.Env == "" {
		cfg.Env = Sandbox
	}
	if cfg.Env != Sandbox && cfg.Env != Production {
		return nil, fmt.Errorf("mpesa: unknown environment %q", cfg.Env)
	}
	base := cfg.BaseURL
	if base == "" {
		base = cfg.Env.baseURL()
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{cfg: cfg, base: base, hc: hc, now: time.Now}, nil
}

func (c *Client) String() string { return c.cfg.String() }

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   string `json:"expires_in"` // Daraja sends this as a string
}

// accessToken returns a cached token, refreshing it 60s before expiry.
//
// The refresh holds c.mu for the duration of the token HTTP call. That is a
// deliberate single-flight: concurrent callers block and reuse the result
// instead of stampeding the token endpoint. The trade-off is that a slow or
// hung token endpoint stalls every in-flight API call until the HTTP
// client's timeout elapses; set a tighter HTTPClient.Timeout if that matters.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && c.now().Before(c.tokenExp) {
		return c.token, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base+"/oauth/v1/generate?grant_type=client_credentials", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.cfg.ConsumerKey, c.cfg.ConsumerSecret)

	body, status, err := c.send(req)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", parseAPIError(status, body)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return "", fmt.Errorf("mpesa: bad token response: %s", truncate(body))
	}
	secs, err := strconv.Atoi(tr.ExpiresIn)
	if err != nil || secs <= 0 {
		secs = 3599
	}
	c.token = tr.AccessToken
	c.tokenExp = c.now().Add(time.Duration(secs)*time.Second - 60*time.Second)
	return c.token, nil
}

func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.token, c.tokenExp = "", time.Time{}
	c.mu.Unlock()
}

// maxAttempts returns the total number of attempts (first try + retries) for
// an idempotent request. Non-idempotent requests always get 1.
func (c *Client) maxAttempts(idempotent bool) int {
	if !idempotent {
		return 1
	}
	n := c.cfg.MaxRetries
	if n == 0 {
		n = DefaultMaxRetries
	}
	if n < 0 {
		n = 0
	}
	if n > 5 {
		n = 5
	}
	return 1 + n
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// backoff sleeps a randomized exponential delay before retry attempt n
// (1-based): 500ms, 1s, 2s, 4s, capped at 8s, ±25% jitter.
func backoff(ctx context.Context, attempt int) error {
	d := 500 * time.Millisecond << uint(min(attempt-1, 4))
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	d += time.Duration(rand.Float64() * 0.5 * float64(d)) // +0..50% jitter
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// postJSON POSTs in as JSON and decodes the response into out. On a 401 it
// drops the cached token and retries once. When idempotent is true, network
// errors, 429s and 5xxs are retried up to MaxRetries times with backoff —
// only ever use it for requests that are safe to repeat.
func (c *Client) postJSON(ctx context.Context, path string, in, out any, idempotent bool) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}

	maxAttempts := c.maxAttempts(idempotent)
	refreshed := false
	for attempt := 1; ; attempt++ {
		tok, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")

		body, status, err := c.send(req)
		switch {
		case err != nil:
			if attempt < maxAttempts {
				if werr := backoff(ctx, attempt); werr != nil {
					return werr
				}
				continue
			}
			return err
		case status == http.StatusUnauthorized && !refreshed:
			// Token expired server-side between our refresh and this call:
			// drop the cache and replay once. Does not consume an attempt.
			refreshed = true
			c.invalidateToken()
			attempt--
			continue
		case retryableStatus(status) && attempt < maxAttempts && !hasErrorCode(body):
			// Empty/HTML 5xx bodies point at a proxy or gateway hiccup;
			// a body with a Daraja errorCode is an application-level
			// answer (e.g. "still processing") and is not retried.
			if werr := backoff(ctx, attempt); werr != nil {
				return werr
			}
			continue
		case status < 200 || status >= 300:
			return parseAPIError(status, body)
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("mpesa: bad response: %s", truncate(body))
		}
		return nil
	}
}

func (c *Client) send(req *http.Request) ([]byte, int, error) {
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}

// hasErrorCode reports whether body is a Daraja error envelope with an
// errorCode, i.e. Daraja itself answered rather than a proxy failing.
func hasErrorCode(body []byte) bool {
	var e struct {
		ErrorCode string `json:"errorCode"`
	}
	return json.Unmarshal(body, &e) == nil && e.ErrorCode != ""
}

func parseAPIError(status int, body []byte) error {
	var e struct {
		RequestID    string `json:"requestId"`
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	}
	_ = json.Unmarshal(body, &e)
	if e.ErrorMessage == "" {
		e.ErrorMessage = truncate(body)
	}
	return &APIError{HTTPStatus: status, RequestID: e.RequestID, Code: e.ErrorCode, Message: e.ErrorMessage}
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}

// password builds the STK password: base64(shortcode + passkey + timestamp).
func (c *Client) password(ts string) string {
	return base64.StdEncoding.EncodeToString([]byte(c.cfg.Shortcode + c.cfg.Passkey + ts))
}

func (c *Client) timestamp() string { return c.now().In(eat).Format("20060102150405") }

// credential returns the SecurityCredential for initiator API calls, building
// and caching it from InitiatorPassword + CertificatePEM on first use.
func (c *Client) credential() (string, error) {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	if c.cred != "" || c.credErr != nil {
		return c.cred, c.credErr
	}
	if c.cfg.SecurityCredential != "" {
		c.cred = c.cfg.SecurityCredential
		return c.cred, nil
	}
	if c.cfg.InitiatorPassword == "" || len(c.cfg.CertificatePEM) == 0 {
		c.credErr = errors.New("mpesa: initiator APIs need SecurityCredential, or InitiatorPassword + CertificatePEM")
		return "", c.credErr
	}
	c.cred, c.credErr = EncryptSecurityCredential(c.cfg.InitiatorPassword, c.cfg.CertificatePEM)
	return c.cred, c.credErr
}

// requireInitiator validates the config shared by B2C, TransactionStatus,
// AccountBalance and Reversal, and resolves the SecurityCredential.
func (c *Client) requireInitiator() (string, error) {
	if c.cfg.InitiatorName == "" {
		return "", errors.New("mpesa: InitiatorName is required for initiator APIs")
	}
	return c.credential()
}

// orDefault returns fallback when s is empty.
func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
