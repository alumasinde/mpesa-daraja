<?php require __DIR__ . '/../src/bootstrap.php'; $sandbox = env('MPESA_ENV', 'sandbox') !== 'production'; ?>
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Pay with M-Pesa</title>
  <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet">
  <style>
    body { background: #f2f6f3; }
    .pay-card { max-width: 440px; margin: 6vh auto; }
    .brand { background: #00a550; color: #fff; border-radius: .75rem .75rem 0 0; padding: 1.25rem 1.5rem; }
    .btn-mpesa { background: #00a550; border-color: #00a550; color: #fff; }
    .btn-mpesa:hover { background: #008a43; border-color: #008a43; color: #fff; }
    .state { text-align: center; padding: 1.5rem 0; }
    .state .icon { font-size: 3rem; line-height: 1; }
  </style>
</head>
<body>
<div class="container">
  <div class="pay-card card shadow-sm border-0">
    <div class="brand">
      <h1 class="h4 mb-0">Pay with M-Pesa</h1>
      <small>Secure prompt sent to your phone</small>
    </div>
    <div class="card-body p-4">
      <?php if ($sandbox): ?><div class="alert alert-warning py-2 small">Sandbox mode: no real money moves.</div><?php endif; ?>

      <form id="payForm" novalidate>
        <div class="mb-3">
          <label class="form-label" for="phone">M-Pesa phone number</label>
          <input class="form-control form-control-lg" id="phone" name="phone" inputmode="tel" placeholder="0712 345 678" required autocomplete="tel">
          <div class="invalid-feedback">Enter a valid Safaricom number, e.g. 0712345678.</div>
        </div>
        <div class="mb-3">
          <label class="form-label" for="amount">Amount (KES)</label>
          <input class="form-control form-control-lg" id="amount" name="amount" type="number" min="1" max="250000" step="1" placeholder="500" required>
          <div class="invalid-feedback">Enter a whole amount of at least 1.</div>
        </div>
        <div class="mb-3">
          <label class="form-label" for="reference">Reference <span class="text-muted small">(invoice / account, max 12)</span></label>
          <input class="form-control" id="reference" name="reference" maxlength="12" placeholder="INV-1042" required>
          <div class="invalid-feedback">A reference is required (max 12 characters).</div>
        </div>
        <button class="btn btn-mpesa btn-lg w-100" id="payBtn" type="submit">Pay now</button>
      </form>

      <div id="waiting" class="state d-none">
        <div class="spinner-border text-success mb-3" role="status"></div>
        <p class="mb-1 fw-semibold">Check your phone</p>
        <p class="text-muted small mb-0">Enter your M-Pesa PIN to approve the payment.</p>
      </div>
      <div id="done" class="state d-none">
        <div class="icon mb-2" id="doneIcon"></div>
        <p class="fw-semibold mb-1" id="doneTitle"></p>
        <p class="text-muted small mb-3" id="doneMsg"></p>
        <button class="btn btn-outline-secondary" id="again" type="button">Make another payment</button>
      </div>
      <div id="err" class="alert alert-danger mt-3 d-none" role="alert"></div>
    </div>
  </div>
</div>

<script>
const $ = id => document.getElementById(id);
const show = (id, on) => $(id).classList.toggle('d-none', !on);
let timer = null;

function reset() {
  clearTimeout(timer);
  show('payForm', true); show('waiting', false); show('done', false); show('err', false);
  $('payBtn').disabled = false;
}
function showError(msg) { $('err').textContent = msg; show('err', true); }

function finish(status, receipt, message) {
  show('waiting', false); show('done', true);
  const ok = status === 'paid';
  $('doneIcon').textContent = ok ? '✅' : (status === 'cancelled' ? '↩️' : '❌');
  $('doneTitle').textContent = ok ? 'Payment received' : (status === 'cancelled' ? 'Payment cancelled' : 'Payment failed');
  $('doneMsg').textContent = ok ? 'M-Pesa receipt: ' + receipt : (message || 'You were not charged.');
}

async function poll(id, started) {
  if (Date.now() - started > 120000) { // stop after 2 minutes
    show('waiting', false); show('payForm', true); $('payBtn').disabled = false;
    return showError('Still waiting for confirmation. If you were charged, contact us with your reference.');
  }
  try {
    const r = await fetch('api/status.php?id=' + encodeURIComponent(id));
    const j = await r.json();
    if (j.status && j.status !== 'pending') return finish(j.status, j.receipt, j.message);
  } catch (e) { /* transient network error: keep polling */ }
  timer = setTimeout(() => poll(id, started), 3000);
}

$('payForm').addEventListener('submit', async e => {
  e.preventDefault();
  show('err', false);
  const f = e.target;
  const phone = /^(\+?254|0)?[17]\d{8}$/.test(f.phone.value.replace(/[\s-]/g, ''));
  const amount = Number.isInteger(+f.amount.value) && +f.amount.value >= 1;
  const ref = f.reference.value.trim().length >= 1;
  f.phone.classList.toggle('is-invalid', !phone);
  f.amount.classList.toggle('is-invalid', !amount);
  f.reference.classList.toggle('is-invalid', !ref);
  if (!(phone && amount && ref)) return;

  $('payBtn').disabled = true;
  try {
    const r = await fetch('api/stk.php', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ phone: f.phone.value, amount: +f.amount.value,
        reference: f.reference.value.trim(), description: 'Payment' })
    });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || 'Could not start payment');
    show('payForm', false); show('waiting', true);
    poll(j.checkout_request_id, Date.now());
  } catch (err) {
    showError(err.message); $('payBtn').disabled = false;
  }
});
$('again').addEventListener('click', () => { $('payForm').reset(); reset(); });
</script>
</body>
</html>
