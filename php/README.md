# M-Pesa Daraja for PHP

Dependency-free PHP 8 client for Safaricom Daraja (port of your Go `mpesa-daraja`)
plus a ready payment page, callbacks and an admin list. Needs ext-curl, ext-openssl, PDO MySQL.

Covers: STK Push + query, C2B register/validation/confirmation, B2C, transaction status,
account balance, reversal.

## Setup
1. Copy the folder to your host; point the web root at `public/` (keep `src/`, `.env`, `storage/` above it).
2. `cp .env.example .env` and fill in Daraja keys, shortcode, passkey, DB, public callback URLs.
3. Import `schema.sql` into MySQL.
4. `storage/` must be writable by PHP (token cache + `mpesa.log`).
5. Open `/` for the payment page, `/admin.php` for transactions (set `ADMIN_PASSWORD`).

Locally, expose callbacks with ngrok/cloudflared; Safaricom only calls public HTTPS URLs.

## Use in your own projects
```php
require 'src/bootstrap.php';          // or require src/Support.php + src/Client.php
$ack = mpesa()->stkPush(['phone'=>'0712345678','amount'=>500,'reference'=>'INV-1042','description'=>'Rent']);
// store $ack['CheckoutRequestID'] against your order; the callback settles it
```
Till number: `MPESA_TXN_TYPE=CustomerBuyGoodsOnline`, `MPESA_PARTY_B=<till>`.
Put the `Client` calls inside your Services layer and `settle_transaction()` logic in your Repository.

## Safety built in
- Callbacks are unsigned: amount is checked against the stored request; only `pending` rows update (idempotent).
- Status endpoint falls back to STK Query if the callback is late.
- Money-moving calls (STK push, B2C, reversal) are never auto-retried.
- Optional `CALLBACK_ALLOWED_IPS` allowlist. Never commit `.env`.

## Go-live checklist
Production keys + shortcode + passkey, HTTPS callbacks (C2B URLs may not contain "mpesa"),
C2B URLs registered via `php bin/register-c2b.php https://your.host/api`, initiator + certificate for B2C.
