// Package mpesa is a small, dependency-free client for Safaricom's Daraja API.
//
// It covers OAuth token caching, STK Push (Lipa na M-Pesa Online), STK query,
// C2B URL registration, and parsing of STK/C2B callbacks.
package mpesa

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

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
type Config struct {
	ConsumerKey    string
	ConsumerSecret string

	// Shortcode is the paybill / store number (BusinessShortCode).
	Shortcode string
	// Passkey is the Lipa na M-Pesa Online passkey for Shortcode.
	Passkey string
	// CallbackURL is the default STK callback URL (overridable per request).
	CallbackURL string

	Env Environment // defaults to Sandbox

	// HTTPClient is optional; defaults to a client with a 30s timeout.
	HTTPClient *http.Client
	// BaseURL overrides the Daraja host (tests, proxies).
	BaseURL string
}

// Client is safe for concurrent use.
type Client struct {
	cfg  Config
	base string
	hc   *http.Client
	now  func() time.Time

	mu       sync.Mutex
	token    string
	tokenExp time.Time
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

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   string `json:"expires_in"` // Daraja sends this as a string
}

// accessToken returns a cached token, refreshing it 60s before expiry.
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

// postJSON POSTs in as JSON and decodes the response into out. On a 401 it
// drops the cached token and retries once.
func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}

	for attempt := 0; attempt < 2; attempt++ {
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
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized && attempt == 0 {
			c.invalidateToken()
			continue
		}
		if status < 200 || status >= 300 {
			return parseAPIError(status, body)
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("mpesa: bad response: %s", truncate(body))
		}
		return nil
	}
	return errors.New("mpesa: unauthorized after token refresh")
}

func (c *Client) send(req *http.Request) ([]byte, int, error) {
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
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
