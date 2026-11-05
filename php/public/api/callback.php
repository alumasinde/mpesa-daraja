<?php
declare(strict_types=1);
require __DIR__ . '/../../src/bootstrap.php';

use Alba\Mpesa\Callbacks;

guard_callback_ip();
$raw = (string)file_get_contents('php://input');
log_line('stk callback received');

try {
    $cb = Callbacks::parseStk($raw);
} catch (\InvalidArgumentException $e) {
    http_response_code(400);
    exit;
}

try {
    $st = db()->prepare('SELECT amount FROM mpesa_transactions WHERE checkout_request_id=?');
    $st->execute([$cb['checkout_request_id']]);
    $row = $st->fetch();
    if ($row) {
        // Callbacks are unsigned: never trust a "paid" whose amount differs from what we asked for.
        if ($cb['result_code'] === 0 && $cb['amount'] !== (int)$row['amount']) {
            log_line("amount mismatch on {$cb['checkout_request_id']}: expected {$row['amount']} got {$cb['amount']}");
            $cb['result_code'] = -2;
            $cb['result_desc'] = 'Amount mismatch';
        }
        settle_transaction($cb['checkout_request_id'], $cb['result_code'], $cb['result_desc'], $cb['receipt'], $cb['amount']);
    } else {
        log_line('callback for unknown checkout id');
    }
} catch (\Throwable $e) {
    log_line('callback db error: ' . $e->getMessage());
    http_response_code(500);
    exit;
}

header('Content-Type: application/json');
echo '{"ResultCode":0,"ResultDesc":"Accepted"}';
