<?php
declare(strict_types=1);
// ResultURL for B2C / Transaction Status / Account Balance / Reversal.
require __DIR__ . '/../../src/bootstrap.php';

use Alba\Mpesa\Callbacks;

guard_callback_ip();
try {
    $r = Callbacks::parseResult((string)file_get_contents('php://input'));
} catch (\InvalidArgumentException) {
    http_response_code(400);
    exit;
}
// Customise: persist $r against your own record, keyed by conversation_id / transaction_id.
log_line(sprintf('result conv=%s code=%d desc=%s', $r['conversation_id'], $r['result_code'], $r['result_desc']));
header('Content-Type: application/json');
echo '{"ResultCode":0,"ResultDesc":"Accepted"}';
