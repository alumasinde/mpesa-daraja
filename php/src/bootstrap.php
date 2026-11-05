<?php
declare(strict_types=1);

require_once __DIR__ . '/Support.php';
require_once __DIR__ . '/Client.php';

use Alba\Mpesa\Client;

date_default_timezone_set('Africa/Nairobi');

/** Load .env (KEY=VALUE, # comments, optional quotes) once. */
function env(string $key, string $default = ''): string
{
    static $vars = null;
    if ($vars === null) {
        $vars = [];
        $file = getenv('MPESA_ENV_FILE') ?: dirname(__DIR__) . '/.env';
        if (is_file($file)) {
            foreach (file($file, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
                $line = trim($line);
                if ($line === '' || $line[0] === '#' || !str_contains($line, '=')) continue;
                [$k, $v] = explode('=', $line, 2);
                $vars[trim($k)] = trim(trim($v), "\"'");
            }
        }
    }
    $real = getenv($key);
    return $vars[$key] ?? ($real !== false ? $real : $default);
}

function mpesa(): Client
{
    static $c = null;
    return $c ??= new Client([
        'env' => env('MPESA_ENV', 'sandbox'),
        'base_url' => env('MPESA_BASE_URL'),
        'consumer_key' => env('MPESA_CONSUMER_KEY'),
        'consumer_secret' => env('MPESA_CONSUMER_SECRET'),
        'shortcode' => env('MPESA_SHORTCODE'),
        'passkey' => env('MPESA_PASSKEY'),
        'callback_url' => env('MPESA_CALLBACK_URL'),
        'result_url' => env('MPESA_RESULT_URL'),
        'timeout_url' => env('MPESA_TIMEOUT_URL'),
        'initiator_name' => env('MPESA_INITIATOR_NAME'),
        'initiator_password' => env('MPESA_INITIATOR_PASSWORD'),
        'certificate_path' => env('MPESA_CERT_PATH'),
        'token_cache_dir' => dirname(__DIR__) . '/storage',
    ]);
}

function db(): PDO
{
    static $pdo = null;
    return $pdo ??= new PDO(env('DB_DSN'), env('DB_USER') ?: null, env('DB_PASS') ?: null, [
        PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
        PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
    ]);
}

function json_out(array $data, int $status = 200): never
{
    http_response_code($status);
    header('Content-Type: application/json');
    echo json_encode($data);
    exit;
}

/** Log without ever including secrets. */
function log_line(string $msg): void
{
    @file_put_contents(dirname(__DIR__) . '/storage/mpesa.log', date('c') . ' ' . $msg . "\n", FILE_APPEND | LOCK_EX);
}

/** Optional IP allowlist for callback endpoints (CALLBACK_ALLOWED_IPS). */
function guard_callback_ip(): void
{
    $allowed = array_filter(array_map('trim', explode(',', env('CALLBACK_ALLOWED_IPS'))));
    if ($allowed && !in_array($_SERVER['REMOTE_ADDR'] ?? '', $allowed, true)) {
        log_line('callback rejected from ' . ($_SERVER['REMOTE_ADDR'] ?? '?'));
        http_response_code(403);
        exit;
    }
}

/** STK query result -> transaction status string, or null if still pending. */
function status_from_code(int $code): string
{
    return match (true) {
        $code === 0 => 'paid',
        $code === 1032 => 'cancelled',
        default => 'failed',
    };
}

/**
 * Idempotently settle a pending transaction. Only a 'pending' row is updated,
 * so duplicate callbacks and callback/poll races cannot double-apply.
 */
function settle_transaction(string $checkoutId, int $resultCode, string $desc, string $receipt = '', int $paidAmount = 0): bool
{
    $status = status_from_code($resultCode);
    $now = date('Y-m-d H:i:s');
    $st = db()->prepare(
        "UPDATE mpesa_transactions SET status=?, result_code=?, result_desc=?, mpesa_receipt=?,
         paid_amount=?, paid_at=?, updated_at=? WHERE checkout_request_id=? AND status='pending'"
    );
    $st->execute([$status, $resultCode, mb_substr($desc, 0, 255), $receipt ?: null,
        $status === 'paid' ? $paidAmount : null, $status === 'paid' ? $now : null, $now, $checkoutId]);
    return $st->rowCount() === 1;
}
