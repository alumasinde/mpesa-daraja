# mpesa

Small, dependency-free Go client for Safaricom Daraja: STK Push, STK query,
C2B URL registration, and callback parsing. Standard library only.

## Install

```bash
# 1. replace YOUR_USER in go.mod and in the import path below
go mod edit -module github.com/alumasinde/mpesa-daraja
git init && git add . && git commit -m "mpesa v0.1.0"
git remote add origin git@github.com:<you>/mpesa-daraja.git
git tag v0.1.0 && git push origin main v0.1.0

# in a consuming project
go get github.com/alumasinde/mpesa-daraja@v0.1.0
```

Private repo: `go env -w GOPRIVATE=github.com/<you>/*` and use SSH or a token.

## Usage

```go
c, err := mpesa.New(mpesa.Config{
    ConsumerKey:    os.Getenv("MPESA_KEY"),
    ConsumerSecret: os.Getenv("MPESA_SECRET"),
    Shortcode:      os.Getenv("MPESA_SHORTCODE"),
    Passkey:        os.Getenv("MPESA_PASSKEY"),
    CallbackURL:    "https://pay.example.co.ke/mpesa/stk",
    Env:            mpesa.Production, // default is Sandbox
})

resp, err := c.STKPush(ctx, mpesa.STKPushRequest{
    Phone:            "0712 345 678",
    Amount:           500,
    AccountReference: "INV-1042", // max 12 chars
    Description:      "Rent",     // max 13 chars
})
// store resp.CheckoutRequestID against your order

http.Handle("/mpesa/stk", mpesa.CallbackHandler(
    func(ctx context.Context, cb *mpesa.STKCallback) error {
        if !cb.Success() { return markFailed(ctx, cb.CheckoutRequestID, cb.ResultDesc) }
        return markPaid(ctx, cb.CheckoutRequestID, cb.MpesaReceiptNumber, cb.Amount)
    }))
```

Till number (Buy Goods): set `TransactionType: mpesa.BuyGoods` and
`PartyB: "<till number>"`; `Config.Shortcode` is then the store/head-office number.

Fallback if a callback never arrives:

```go
q, err := c.STKQuery(ctx, checkoutRequestID)
if mpesa.IsProcessing(err) { /* customer still deciding; retry in a few seconds */ }
if err == nil && q.Paid() { /* confirmed */ }
```

## Notes

- One `Client` per shortcode. It is safe for concurrent use and caches the
  OAuth token (refreshed 60s early, and once on a 401).
- Timestamps use East Africa Time as Daraja requires.
- Callbacks are not signed. Make your handler idempotent (dedupe on
  `CheckoutRequestID`), confirm large payments with `STKQuery`, and restrict
  the endpoint to Safaricom IPs at your proxy if you can.
- Never log consumer secrets or passkeys. Load them from env/secret storage.
- Field length limits (12/13 chars) and the v2 C2B endpoint follow Daraja's
  docs; re-check them against the portal if Safaricom changes the API.
- Not covered yet: B2C, B2B, reversals, transaction status, account balance.
