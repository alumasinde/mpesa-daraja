<?php
declare(strict_types=1);
require __DIR__ . '/../../src/bootstrap.php';

use Alba\Mpesa\Client;

$id = (string)($_GET['id'] ?? '');
if ($id === '') json_out(['error' => 'id required'], 422);

$fetch = fn() => db()->prepare('SELECT status, amount, mpesa_receipt, result_desc, created_at FROM mpesa_transactions WHERE checkout_request_id=?');
$st = $fetch(); $st->execute([$id]);
$row = $st->fetch();
if (!$row) json_out(['error' => 'not found'], 404);

// Fallback when the callback is slow/lost: ask Daraja once the row is >20s old.
if ($row['status'] === 'pending' && time() - strtotime($row['created_at']) > 20) {
    try {
        $q = mpesa()->stkQuery($id);
        if (isset($q['ResultCode']) && $q['ResultCode'] !== '') {
            settle_transaction($id, (int)$q['ResultCode'], (string)($q['ResultDesc'] ?? ''), '', (int)$row['amount']);
        }
    } catch (\Throwable $e) {
        if (!Client::isProcessing($e)) log_line('status query: ' . $e->getMessage());
    }
    $st = $fetch(); $st->execute([$id]);
    $row = $st->fetch();
}

json_out([
    'status' => $row['status'],
    'receipt' => $row['mpesa_receipt'],
    'message' => $row['result_desc'],
]);
