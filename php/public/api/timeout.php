<?php
declare(strict_types=1);
require __DIR__ . '/../../src/bootstrap.php';
guard_callback_ip();
log_line('queue timeout received');
header('Content-Type: application/json');
echo '{"ResultCode":0,"ResultDesc":"Accepted"}';
