<?php
declare(strict_types=1);
require __DIR__ . '/../../src/bootstrap.php';

use Alba\Mpesa\{Phone, MpesaException};

if ($_SERVER['REQUEST_METHOD'] !== 'POST') json_out(['error' => 'POST only'], 405);

$in = json_decode((string)file_get_contents('php://input'), true) ?: $_POST;
$amount = (int)($in['amount'] ?? 0);
$ref = trim((string)($in['reference'] ?? ''));
$desc = trim((string)($in['description'] ?? 'Payment'));

try {
    $phone = Phone::normalize((string)($in['phone'] ?? ''));
    if ($amount < 1 || $amount > 250000) json_out(['error' => 'Amount must be between 1 and 250,000'], 422);

    $ack = mpesa()->stkPush([
        'phone' => $phone, 'amount' => $amount, 'reference' => $ref, 'description' => $desc,
        'type' => env('MPESA_TXN_TYPE', 'CustomerPayBillOnline'),
        'party_b' => env('MPESA_PARTY_B'),
    ]);

    $now = date('Y-m-d H:i:s');
    db()->prepare(
        "INSERT INTO mpesa_transactions (checkout_request_id, merchant_request_id, phone, amount,
         account_reference, description, status, created_at, updated_at) VALUES (?,?,?,?,?,?, 'pending', ?, ?)"
    )->execute([$ack['CheckoutRequestID'], $ack['MerchantRequestID'] ?? null, $phone, $amount, $ref, $desc, $now, $now]);

    json_out(['ok' => true, 'checkout_request_id' => $ack['CheckoutRequestID'],
        'message' => $ack['CustomerMessage'] ?? 'Check your phone for the M-Pesa prompt']);
} catch (\InvalidArgumentException $e) {
    json_out(['error' => $e->getMessage()], 422);
} catch (MpesaException $e) {
    log_line('stk error: ' . $e->getMessage());
    json_out(['error' => 'Could not start the payment. Please try again.'], 502);
} catch (\Throwable $e) {
    log_line('stk fatal: ' . $e->getMessage());
    json_out(['error' => 'Server error'], 500);
}
