<?php
declare(strict_types=1);
// Usage: php bin/register-c2b.php https://pay.example.co.ke/api
// Daraja rejects URLs containing words like "mpesa" or "safaricom".
require __DIR__ . '/../src/bootstrap.php';

$base = rtrim($argv[1] ?? '', '/');
if ($base === '') { fwrite(STDERR, "usage: php bin/register-c2b.php <https-base-url>\n"); exit(1); }
print_r(mpesa()->registerC2BUrls("$base/c2b_confirmation.php", "$base/c2b_validation.php"));
