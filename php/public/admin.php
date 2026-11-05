<?php
declare(strict_types=1);
require __DIR__ . '/../src/bootstrap.php';
session_start(['cookie_httponly' => true, 'cookie_samesite' => 'Strict']);

$pw = env('ADMIN_PASSWORD');
if ($pw === '') { http_response_code(404); exit('Admin disabled. Set ADMIN_PASSWORD in .env.'); }

$h = fn($s) => htmlspecialchars((string)$s, ENT_QUOTES, 'UTF-8');
$error = '';
if (isset($_GET['logout'])) { session_destroy(); header('Location: admin.php'); exit; }
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    if (hash_equals($pw, (string)($_POST['password'] ?? ''))) {
        session_regenerate_id(true); $_SESSION['admin'] = true; header('Location: admin.php'); exit;
    }
    $error = 'Wrong password'; usleep(500000);
}
$authed = !empty($_SESSION['admin']);

$rows = $totals = [];
if ($authed) {
    $status = $_GET['status'] ?? '';
    $sql = 'SELECT * FROM mpesa_transactions' . (in_array($status, ['pending','paid','failed','cancelled'], true) ? ' WHERE status=' . db()->quote($status) : '') . ' ORDER BY id DESC LIMIT 200';
    $rows = db()->query($sql)->fetchAll();
    $totals = db()->query("SELECT COUNT(*) n, COALESCE(SUM(CASE WHEN status='paid' THEN paid_amount END),0) paid FROM mpesa_transactions")->fetch();
}
$badge = ['paid' => 'success', 'pending' => 'warning', 'failed' => 'danger', 'cancelled' => 'secondary'];
?>
<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>M-Pesa transactions</title>
<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet"></head>
<body class="bg-light"><div class="container py-4">
<?php if (!$authed): ?>
  <form method="post" class="card p-4 mx-auto shadow-sm" style="max-width:360px">
    <h1 class="h5 mb-3">Admin sign in</h1>
    <?php if ($error): ?><div class="alert alert-danger py-2"><?= $h($error) ?></div><?php endif; ?>
    <input type="password" name="password" class="form-control mb-3" placeholder="Password" autofocus required>
    <button class="btn btn-dark">Sign in</button>
  </form>
<?php else: ?>
  <div class="d-flex justify-content-between align-items-center mb-3">
    <h1 class="h4 mb-0">M-Pesa transactions</h1><a href="?logout=1" class="btn btn-sm btn-outline-secondary">Sign out</a>
  </div>
  <div class="row g-3 mb-3">
    <div class="col-6 col-md-3"><div class="card p-3"><div class="text-muted small">Total attempts</div><div class="fs-4"><?= (int)$totals['n'] ?></div></div></div>
    <div class="col-6 col-md-3"><div class="card p-3"><div class="text-muted small">Collected (KES)</div><div class="fs-4"><?= number_format((int)$totals['paid']) ?></div></div></div>
  </div>
  <div class="mb-3 btn-group btn-group-sm">
    <?php foreach (['' => 'All', 'paid' => 'Paid', 'pending' => 'Pending', 'failed' => 'Failed', 'cancelled' => 'Cancelled'] as $k => $l): ?>
      <a class="btn btn-outline-dark <?= ($_GET['status'] ?? '') === (string)$k ? 'active' : '' ?>" href="?status=<?= $k ?>"><?= $l ?></a>
    <?php endforeach; ?>
  </div>
  <div class="table-responsive card"><table class="table table-sm table-hover mb-0 align-middle">
    <thead><tr><th>Time</th><th>Phone</th><th class="text-end">Amount</th><th>Reference</th><th>Receipt</th><th>Status</th><th>Note</th></tr></thead>
    <tbody>
    <?php foreach ($rows as $r): ?>
      <tr><td><?= $h($r['created_at']) ?></td><td><?= $h(substr($r['phone'], 0, 6) . '***' . substr($r['phone'], -3)) ?></td>
        <td class="text-end"><?= number_format((int)$r['amount']) ?></td><td><?= $h($r['account_reference']) ?></td>
        <td><?= $h($r['mpesa_receipt']) ?></td>
        <td><span class="badge text-bg-<?= $badge[$r['status']] ?? 'secondary' ?>"><?= $h($r['status']) ?></span></td>
        <td class="small text-muted"><?= $h($r['result_desc']) ?></td></tr>
    <?php endforeach; if (!$rows): ?><tr><td colspan="7" class="text-center text-muted py-4">No transactions yet</td></tr><?php endif; ?>
    </tbody></table></div>
<?php endif; ?>
</div></body></html>
