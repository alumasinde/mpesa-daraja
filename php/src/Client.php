<?php
declare(strict_types=1);

namespace Alba\Mpesa;

/**
 * Small dependency-free Safaricom Daraja client (PHP 8+, ext-curl, ext-openssl).
 * Port of the Go mpesa-daraja library: STK Push/Query, C2B register,
 * B2C, Transaction Status, Account Balance, Reversal.
 */
final class Client
{
    public const SANDBOX = 'https://sandbox.safaricom.co.ke';
    public const PRODUCTION = 'https://api.safaricom.co.ke';
    private const CODE_PROCESSING = '500.001.1001';

    private array $c;
    private string $base;
    private ?string $token = null;
    private int $tokenExp = 0;

    public function __construct(array $config)
    {
        $this->c = $config + [
            'env' => 'sandbox', 'base_url' => '', 'shortcode' => '', 'passkey' => '',
            'callback_url' => '', 'result_url' => '', 'timeout_url' => '',
            'initiator_name' => '', 'initiator_password' => '', 'security_credential' => '',
            'certificate_path' => '', 'max_retries' => 2, 'timeout' => 30,
            'token_cache_dir' => sys_get_temp_dir(),
        ];
        if (empty($this->c['consumer_key']) || empty($this->c['consumer_secret'])) {
            throw new \InvalidArgumentException('consumer_key and consumer_secret are required');
        }
        if (!in_array($this->c['env'], ['sandbox', 'production'], true)) {
            throw new \InvalidArgumentException("unknown env {$this->c['env']}");
        }
        $this->base = $this->c['base_url'] ?: ($this->c['env'] === 'production' ? self::PRODUCTION : self::SANDBOX);
    }

    public static function isProcessing(\Throwable $e): bool
    {
        return $e instanceof ApiException && $e->errorCode === self::CODE_PROCESSING;
    }

    // ---------------------------------------------------------------- STK

    /**
     * @param array{phone:string,amount:int,reference:string,description:string,
     *   callback_url?:string,type?:string,party_b?:string} $r
     * @return array Daraja acknowledgement (MerchantRequestID, CheckoutRequestID, ...)
     */
    public function stkPush(array $r): array
    {
        $this->requireStk();
        $phone = Phone::normalize((string)($r['phone'] ?? ''));
        $amount = (int)($r['amount'] ?? 0);
        $ref = (string)($r['reference'] ?? '');
        $desc = (string)($r['description'] ?? '');
        if ($amount < 1) throw new \InvalidArgumentException('amount must be at least 1');
        if ($ref === '' || strlen($ref) > 12) throw new \InvalidArgumentException('reference must be 1-12 characters');
        if ($desc === '' || strlen($desc) > 13) throw new \InvalidArgumentException('description must be 1-13 characters');
        $cb = $r['callback_url'] ?? $this->c['callback_url'];
        if (!$cb) throw new \InvalidArgumentException('callback_url is required');

        $ts = $this->timestamp();
        $body = [
            'BusinessShortCode' => $this->c['shortcode'],
            'Password' => $this->password($ts),
            'Timestamp' => $ts,
            'TransactionType' => $r['type'] ?? 'CustomerPayBillOnline',
            'Amount' => $amount,
            'PartyA' => $phone,
            'PartyB' => ($r['party_b'] ?? '') ?: $this->c['shortcode'],
            'PhoneNumber' => $phone,
            'CallBackURL' => $cb,
            'AccountReference' => $ref,
            'TransactionDesc' => $desc,
        ];
        // Never retried: a repeat could prompt the customer twice.
        $resp = $this->post('/mpesa/stkpush/v1/processrequest', $body, false);
        if (($resp['ResponseCode'] ?? '') !== '0') throw RejectedException::fromResponse($resp);
        return $resp;
    }

    /** Check an STK push outcome. Throws ApiException (see isProcessing) while pending. */
    public function stkQuery(string $checkoutRequestId): array
    {
        $this->requireStk();
        if ($checkoutRequestId === '') throw new \InvalidArgumentException('checkoutRequestId required');
        $ts = $this->timestamp();
        return $this->post('/mpesa/stkpushquery/v1/query', [
            'BusinessShortCode' => $this->c['shortcode'],
            'Password' => $this->password($ts),
            'Timestamp' => $ts,
            'CheckoutRequestID' => $checkoutRequestId,
        ], true);
    }

    // ---------------------------------------------------------------- C2B

    public function registerC2BUrls(string $confirmationUrl, string $validationUrl, string $responseType = 'Completed'): array
    {
        if (!in_array($responseType, ['Completed', 'Cancelled'], true)) {
            throw new \InvalidArgumentException('responseType must be Completed or Cancelled');
        }
        return $this->post('/mpesa/c2b/v2/registerurl', [
            'ShortCode' => $this->c['shortcode'],
            'ResponseType' => $responseType,
            'ConfirmationURL' => $confirmationUrl,
            'ValidationURL' => $validationUrl,
        ], true);
    }

    // ------------------------------------------------- initiator APIs

    /** @param array{amount:int,phone:string,remarks:string,occasion?:string,command?:string,result_url?:string,timeout_url?:string} $r */
    public function b2c(array $r): array
    {
        [$cred, $result, $timeout] = $this->initiator($r);
        if ((int)$r['amount'] < 1) throw new \InvalidArgumentException('amount must be at least 1');
        if (empty($r['remarks'])) throw new \InvalidArgumentException('remarks required');
        return $this->async('/mpesa/b2c/v3/paymentrequest', [
            'InitiatorName' => $this->c['initiator_name'],
            'SecurityCredential' => $cred,
            'CommandID' => $r['command'] ?? 'BusinessPayment',
            'Amount' => (int)$r['amount'],
            'PartyA' => $this->c['shortcode'],
            'PartyB' => Phone::normalize((string)$r['phone']),
            'Remarks' => $r['remarks'],
            'QueueTimeOutURL' => $timeout,
            'ResultURL' => $result,
            'Occasion' => $r['occasion'] ?? '',
        ], false);
    }

    public function transactionStatus(array $r): array
    {
        [$cred, $result, $timeout] = $this->initiator($r);
        if (empty($r['transaction_id']) || empty($r['remarks'])) {
            throw new \InvalidArgumentException('transaction_id and remarks required');
        }
        return $this->async('/mpesa/transactionstatus/v1/query', [
            'InitiatorName' => $this->c['initiator_name'],
            'SecurityCredential' => $cred,
            'CommandID' => 'TransactionStatusQuery',
            'TransactionID' => $r['transaction_id'],
            'PartyA' => $r['party_a'] ?? $this->c['shortcode'],
            'IdentifierType' => $r['identifier_type'] ?? '4',
            'ResultURL' => $result,
            'QueueTimeOutURL' => $timeout,
            'Remarks' => $r['remarks'],
            'Occasion' => $r['occasion'] ?? '',
        ], true);
    }

    public function accountBalance(array $r): array
    {
        [$cred, $result, $timeout] = $this->initiator($r);
        if (empty($r['remarks'])) throw new \InvalidArgumentException('remarks required');
        return $this->async('/mpesa/accountbalance/v1/query', [
            'InitiatorName' => $this->c['initiator_name'],
            'SecurityCredential' => $cred,
            'CommandID' => 'AccountBalanceQuery',
            'PartyA' => $r['party'] ?? $this->c['shortcode'],
            'IdentifierType' => $r['identifier_type'] ?? '4',
            'Remarks' => $r['remarks'],
            'QueueTimeOutURL' => $timeout,
            'ResultURL' => $result,
        ], true);
    }

    public function reversal(array $r): array
    {
        [$cred, $result, $timeout] = $this->initiator($r);
        if (empty($r['transaction_id']) || (int)($r['amount'] ?? 0) < 1 || empty($r['remarks'])) {
            throw new \InvalidArgumentException('transaction_id, amount (>=1) and remarks required');
        }
        return $this->async('/mpesa/reversal/v1/request', [
            'InitiatorName' => $this->c['initiator_name'],
            'SecurityCredential' => $cred,
            'CommandID' => 'TransactionReversal',
            'TransactionID' => $r['transaction_id'],
            'Amount' => (int)$r['amount'],
            'ReceiverParty' => $r['receiver'] ?? $this->c['shortcode'],
            'ReceiverIdentifierType' => '4',
            'ResultURL' => $result,
            'QueueTimeOutURL' => $timeout,
            'Remarks' => $r['remarks'],
            'Occasion' => $r['occasion'] ?? '',
        ], false);
    }

    /** RSA-encrypt the initiator password with Safaricom's certificate. */
    public static function encryptCredential(string $password, string $certPem): string
    {
        $key = openssl_pkey_get_public($certPem);
        if ($key === false) throw new \RuntimeException('invalid Safaricom certificate');
        if (!openssl_public_encrypt($password, $enc, $key, OPENSSL_PKCS1_PADDING)) {
            throw new \RuntimeException('credential encryption failed');
        }
        return base64_encode($enc);
    }

    // ------------------------------------------------------------ internals

    private function requireStk(): void
    {
        if (!$this->c['shortcode'] || !$this->c['passkey']) {
            throw new \InvalidArgumentException('shortcode and passkey are required for STK');
        }
    }

    private function timestamp(): string
    {
        return (new \DateTimeImmutable('now', new \DateTimeZone('Africa/Nairobi')))->format('YmdHis');
    }

    private function password(string $ts): string
    {
        return base64_encode($this->c['shortcode'] . $this->c['passkey'] . $ts);
    }

    /** @return array{0:string,1:string,2:string} credential, resultUrl, timeoutUrl */
    private function initiator(array $r): array
    {
        if (!$this->c['initiator_name']) throw new \InvalidArgumentException('initiator_name required');
        if ($this->c['security_credential']) {
            $cred = $this->c['security_credential'];
        } elseif ($this->c['initiator_password'] && $this->c['certificate_path']) {
            $pem = @file_get_contents($this->c['certificate_path']);
            if ($pem === false) throw new \RuntimeException('cannot read certificate_path');
            $cred = self::encryptCredential($this->c['initiator_password'], $pem);
        } else {
            throw new \InvalidArgumentException('need security_credential, or initiator_password + certificate_path');
        }
        $result = ($r['result_url'] ?? '') ?: $this->c['result_url'];
        $timeout = ($r['timeout_url'] ?? '') ?: $this->c['timeout_url'];
        if (!$result || !$timeout) throw new \InvalidArgumentException('result_url and timeout_url required');
        return [$cred, $result, $timeout];
    }

    private function async(string $path, array $body, bool $idempotent): array
    {
        $resp = $this->post($path, $body, $idempotent);
        if (($resp['ResponseCode'] ?? '') !== '0') throw RejectedException::fromResponse($resp);
        return $resp;
    }

    private function accessToken(): string
    {
        if ($this->token && time() < $this->tokenExp) return $this->token;

        $file = rtrim($this->c['token_cache_dir'], '/') . '/mpesa-token-' . substr(sha1($this->base . $this->c['consumer_key']), 0, 16) . '.json';
        $cached = is_file($file) ? json_decode((string)@file_get_contents($file), true) : null;
        if (is_array($cached) && ($cached['exp'] ?? 0) > time()) {
            $this->token = $cached['token'];
            $this->tokenExp = $cached['exp'];
            return $this->token;
        }

        [$status, $body] = $this->http('GET', '/oauth/v1/generate?grant_type=client_credentials', null, [
            'Authorization: Basic ' . base64_encode($this->c['consumer_key'] . ':' . $this->c['consumer_secret']),
        ]);
        if ($status !== 200) throw ApiException::fromBody($status, $body);
        $j = json_decode($body, true);
        if (!is_array($j) || empty($j['access_token'])) {
            throw new \RuntimeException('bad token response: ' . substr($body, 0, 200));
        }
        $secs = (int)($j['expires_in'] ?? 3599);
        if ($secs <= 0) $secs = 3599;
        $this->token = $j['access_token'];
        $this->tokenExp = time() + $secs - 60; // refresh 60s early
        @file_put_contents($file, json_encode(['token' => $this->token, 'exp' => $this->tokenExp]), LOCK_EX);
        @chmod($file, 0600);
        return $this->token;
    }

    private function invalidateToken(): void
    {
        $this->token = null;
        $this->tokenExp = 0;
        $file = rtrim($this->c['token_cache_dir'], '/') . '/mpesa-token-' . substr(sha1($this->base . $this->c['consumer_key']), 0, 16) . '.json';
        @unlink($file);
    }

    private function post(string $path, array $body, bool $idempotent): array
    {
        $payload = json_encode($body, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
        $maxAttempts = $idempotent ? 1 + max(0, min(5, (int)$this->c['max_retries'])) : 1;
        $refreshed = false;

        for ($attempt = 1;; $attempt++) {
            $tok = $this->accessToken();
            try {
                [$status, $raw] = $this->http('POST', $path, $payload, [
                    'Authorization: Bearer ' . $tok,
                    'Content-Type: application/json',
                ]);
            } catch (TransportException $e) {
                if ($attempt < $maxAttempts) { $this->backoff($attempt); continue; }
                throw $e;
            }

            if ($status === 401 && !$refreshed) {
                $refreshed = true;
                $this->invalidateToken();
                $attempt--;
                continue;
            }
            $retryable = $status === 429 || $status >= 500;
            if ($retryable && $attempt < $maxAttempts && !self::hasErrorCode($raw)) {
                $this->backoff($attempt);
                continue;
            }
            if ($status < 200 || $status >= 300) throw ApiException::fromBody($status, $raw);
            $j = json_decode($raw, true);
            if (!is_array($j)) throw new \RuntimeException('bad response: ' . substr($raw, 0, 200));
            return $j;
        }
    }

    private static function hasErrorCode(string $raw): bool
    {
        $j = json_decode($raw, true);
        return is_array($j) && !empty($j['errorCode']);
    }

    private function backoff(int $attempt): void
    {
        $ms = min(8000, 500 * (2 ** min($attempt - 1, 4)));
        usleep((int)(($ms + random_int(0, (int)($ms / 2))) * 1000));
    }

    /** @return array{0:int,1:string} */
    private function http(string $method, string $path, ?string $payload, array $headers): array
    {
        $ch = curl_init($this->base . $path);
        curl_setopt_array($ch, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => (int)$this->c['timeout'],
            CURLOPT_CONNECTTIMEOUT => 10,
            CURLOPT_HTTPHEADER => array_merge($headers, ['User-Agent: mpesa-daraja-php/0.1.0']),
            CURLOPT_MAXFILESIZE => 1 << 20,
        ]);
        if ($payload !== null) curl_setopt($ch, CURLOPT_POSTFIELDS, $payload);
        $body = curl_exec($ch);
        if ($body === false) {
            $err = curl_error($ch);
            curl_close($ch);
            throw new TransportException("mpesa: transport error: $err");
        }
        $status = (int)curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        curl_close($ch);
        return [$status, (string)$body];
    }
}
