<?php
// Tiny fake Daraja for local testing: php -S 127.0.0.1:8801 tests/mock_daraja.php
$p = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);
header('Content-Type: application/json');
$body = json_decode(file_get_contents('php://input'), true) ?: [];
if ($p === '/oauth/v1/generate') {
    if (!str_starts_with($_SERVER['HTTP_AUTHORIZATION'] ?? '', 'Basic ')) { http_response_code(400); exit; }
    echo json_encode(['access_token' => 'tok123', 'expires_in' => '3599']); exit;
}
if (($_SERVER['HTTP_AUTHORIZATION'] ?? '') !== 'Bearer tok123') { http_response_code(401); echo '{"errorCode":"404.001.03","errorMessage":"Invalid Access Token"}'; exit; }
if ($p === '/mpesa/stkpush/v1/processrequest') {
    file_put_contents(sys_get_temp_dir() . '/mock_last_stk.json', json_encode($body));
    echo json_encode(['MerchantRequestID' => 'm-1', 'CheckoutRequestID' => 'ws_CO_' . substr($body['PhoneNumber'], -4) . '_' . $body['Amount'],
        'ResponseCode' => '0', 'ResponseDescription' => 'Success', 'CustomerMessage' => 'Success. Request accepted for processing']); exit;
}
if ($p === '/mpesa/stkpushquery/v1/query') {
    if (str_contains($body['CheckoutRequestID'], 'pend')) { http_response_code(500); echo '{"requestId":"r","errorCode":"500.001.1001","errorMessage":"The transaction is being processed"}'; exit; }
    echo json_encode(['ResponseCode' => '0', 'ResultCode' => '1032', 'ResultDesc' => 'Request cancelled by user']); exit;
}
http_response_code(404); echo '{}';
