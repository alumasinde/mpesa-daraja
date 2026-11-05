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
// Customise: reject unknown account numbers. Return C2B00012 to reject.
$accept = trim((string)($n['BillRefNumber'] ?? '')) !== '';
header('Content-Type: application/json');
echo json_encode([
    'ResultCode' => $accept ? '0' : 'C2B00012',
    'ResultDesc' => $accept ? 'Accepted' : 'Invalid account',
    'ThirdPartyTransID' => $n['TransID'],
]);
