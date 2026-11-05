CREATE TABLE IF NOT EXISTS mpesa_transactions (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  checkout_request_id VARCHAR(64) NOT NULL UNIQUE,
  merchant_request_id VARCHAR(64) NULL,
  phone VARCHAR(15) NOT NULL,
  amount INT UNSIGNED NOT NULL,
  account_reference VARCHAR(12) NOT NULL,
  description VARCHAR(13) NOT NULL,
  status ENUM('pending','paid','failed','cancelled') NOT NULL DEFAULT 'pending',
  result_code INT NULL,
  result_desc VARCHAR(255) NULL,
  mpesa_receipt VARCHAR(20) NULL,
  paid_amount INT UNSIGNED NULL,
  paid_at DATETIME NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  KEY idx_status (status),
  KEY idx_receipt (mpesa_receipt)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS mpesa_c2b_payments (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  trans_id VARCHAR(20) NOT NULL UNIQUE,
  trans_time VARCHAR(14) NULL,
  amount DECIMAL(12,2) NOT NULL,
  shortcode VARCHAR(12) NULL,
  bill_ref VARCHAR(40) NULL,
  msisdn VARCHAR(64) NULL,
  first_name VARCHAR(60) NULL,
  last_name VARCHAR(60) NULL,
  created_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
