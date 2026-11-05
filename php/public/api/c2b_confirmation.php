<?php
declare(strict_types=1);
require __DIR__ . '/../../src/bootstrap.php';

use Alba\Mpesa\Callbacks;

guard_callback_ip();
try {
    $n = Callbacks::parseC2B((string)file_get_contents('php://input'));
} catch (\InvalidArgumentException) {
    http_response_code(400);
    exit;
}
try {
    // trans_id is UNIQUE: a redelivered confirmation is ignored (idempotent).
    $ins = db()->prepare(db()->getAttribute(PDO::ATTR_DRIVER_NAME) === 'mysql'
        ? 'INSERT IGNORE INTO mpesa_c2b_payments (trans_id,trans_time,amount,shortcode,bill_ref,msisdn,first_name,last_name,created_at) VALUES (?,?,?,?,?,?,?,?,?)'
        : 'INSERT OR IGNORE INTO mpesa_c2b_payments (trans_id,trans_time,amount,shortcode,bill_ref,msisdn,first_name,last_name,created_at) VALUES (?,?,?,?,?,?,?,?,?)');
    $ins->execute([$n['TransID'], $n['TransTime'] ?? null, (float)($n['TransAmount'] ?? 0), $n['BusinessShortCode'] ?? null,
        $n['BillRefNumber'] ?? null, $n['MSISDN'] ?? null, $n['FirstName'] ?? null, $n['LastName'] ?? null, date('Y-m-d H:i:s')]);
} catch (\Throwable $e) {
    log_line('c2b db error: ' . $e->getMessage());
    http_response_code(500);
    exit;
}
header('Content-Type: application/json');
echo json_encode(['ResultCode' => '0', 'ResultDesc' => 'Accepted', 'ThirdPartyTransID' => $n['TransID']]);
