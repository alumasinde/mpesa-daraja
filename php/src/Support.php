<?php
declare(strict_types=1);

namespace Alba\Mpesa;

class MpesaException extends \RuntimeException {}

final class TransportException extends MpesaException {}

/** Daraja answered with an HTTP error. */
final class ApiException extends MpesaException
{
    public function __construct(
        string $message,
        public readonly int $httpStatus,
        public readonly string $errorCode = '',
        public readonly string $requestId = ''
    ) {
        parent::__construct($message);
    }

    public static function fromBody(int $status, string $body): self
    {
        $j = json_decode($body, true);
        $j = is_array($j) ? $j : [];
        $msg = (string)($j['errorMessage'] ?? substr($body, 0, 200));
        $code = (string)($j['errorCode'] ?? '');
        $rid = (string)($j['requestId'] ?? '');
        return new self("mpesa: api error (http $status, code $code, request $rid): $msg", $status, $code, $rid);
    }
}

/** HTTP 200 but non-"0" ResponseCode (e.g. STK push rejected). */
final class RejectedException extends MpesaException
{
    public function __construct(
        string $message,
        public readonly string $responseCode,
        public readonly array $response = []
    ) {
        parent::__construct($message);
    }

    public static function fromResponse(array $r): self
    {
        $code = (string)($r['ResponseCode'] ?? '');
        $desc = (string)($r['ResponseDescription'] ?? '');
        return new self("mpesa: request rejected: $desc ($code)", $code, $r);
    }
}

final class Phone
{
    /** Convert 07xx / 01xx / 7xx / +254... to 2547XXXXXXXX / 2541XXXXXXXX. */
    public static function normalize(string $s): string
    {
        $p = ltrim(str_replace([' ', '-', '(', ')'], '', $s), '+');
        if ($p === '' || !ctype_digit($p)) throw new \InvalidArgumentException("invalid phone number '$s'");

        if (str_starts_with($p, '254') && strlen($p) === 12) {
            // canonical
        } elseif (str_starts_with($p, '0') && strlen($p) === 10) {
            $p = '254' . substr($p, 1);
        } elseif (strlen($p) === 9 && ($p[0] === '7' || $p[0] === '1')) {
            $p = '254' . $p;
        } else {
            throw new \InvalidArgumentException("invalid phone number '$s'");
        }
        if ($p[3] !== '7' && $p[3] !== '1') throw new \InvalidArgumentException("invalid phone number '$s'");
        return $p;
    }
}

/** Parsers for what Safaricom POSTs to your URLs. */
final class Callbacks
{
    /**
     * @return array{merchant_request_id:string,checkout_request_id:string,result_code:int,
     *   result_desc:string,amount:int,receipt:string,transaction_date:string,phone:string}
     */
    public static function parseStk(string $raw): array
    {
        $j = json_decode(substr($raw, 0, 65536), true);
        $s = is_array($j) ? ($j['Body']['stkCallback'] ?? null) : null;
        if (!is_array($s) || empty($s['CheckoutRequestID'])) {
            throw new \InvalidArgumentException('callback missing CheckoutRequestID');
        }
        $out = [
            'merchant_request_id' => (string)($s['MerchantRequestID'] ?? ''),
            'checkout_request_id' => (string)$s['CheckoutRequestID'],
            'result_code' => (int)($s['ResultCode'] ?? -1),
            'result_desc' => (string)($s['ResultDesc'] ?? ''),
            'amount' => 0, 'receipt' => '', 'transaction_date' => '', 'phone' => '',
        ];
        foreach (($s['CallbackMetadata']['Item'] ?? []) as $it) {
            $v = $it['Value'] ?? '';
            $v = is_scalar($v) ? (string)$v : '';
            switch ($it['Name'] ?? '') {
                case 'Amount': $out['amount'] = (int)round((float)$v); break;
                case 'MpesaReceiptNumber': $out['receipt'] = $v; break;
                case 'TransactionDate': $out['transaction_date'] = $v; break; // YYYYmmddHHMMSS, EAT
                case 'PhoneNumber': $out['phone'] = $v; break;
            }
        }
        return $out;
    }

    public static function parseC2B(string $raw): array
    {
        $n = json_decode(substr($raw, 0, 65536), true);
        if (!is_array($n) || empty($n['TransID'])) throw new \InvalidArgumentException('c2b notification missing TransID');
        return $n;
    }

    /** Result of B2C / status / balance / reversal. Parameters flattened to Key => string. */
    public static function parseResult(string $raw): array
    {
        $j = json_decode(substr($raw, 0, 65536), true);
        $r = is_array($j) ? ($j['Result'] ?? null) : null;
        if (!is_array($r) || (empty($r['ConversationID']) && empty($r['OriginatorConversationID']))) {
            throw new \InvalidArgumentException('result callback missing conversation IDs');
        }
        $params = [];
        foreach (($r['ResultParameters']['ResultParameter'] ?? []) as $p) {
            $v = $p['Value'] ?? '';
            $params[(string)($p['Key'] ?? '')] = is_scalar($v) ? (string)$v : '';
        }
        return [
            'result_type' => (int)($r['ResultType'] ?? 0),
            'result_code' => (int)($r['ResultCode'] ?? -1),
            'result_desc' => (string)($r['ResultDesc'] ?? ''),
            'originator_conversation_id' => (string)($r['OriginatorConversationID'] ?? ''),
            'conversation_id' => (string)($r['ConversationID'] ?? ''),
            'transaction_id' => (string)($r['TransactionID'] ?? ''),
            'parameters' => $params,
        ];
    }
}
