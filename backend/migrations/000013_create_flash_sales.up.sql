CREATE TABLE flash_sale_campaigns (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    campaign_no VARCHAR(40) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    sku_id BIGINT UNSIGNED NOT NULL,
    title VARCHAR(200) NOT NULL,
    campaign_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    price_cent BIGINT NOT NULL,
    total_stock BIGINT NOT NULL,
    per_user_limit INT UNSIGNED NOT NULL DEFAULT 1,
    starts_at DATETIME(6) NOT NULL,
    ends_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_flash_sale_campaign_no (campaign_no),
    UNIQUE KEY uq_flash_sale_sku_window_version (sku_id, starts_at, ends_at, campaign_version),
    KEY idx_flash_sale_active_window (status, starts_at, ends_at, id),
    KEY idx_flash_sale_sku_page (sku_id, created_at DESC, id DESC),
    CONSTRAINT fk_flash_sale_campaign_sku FOREIGN KEY (sku_id) REFERENCES product_skus (id),
    CONSTRAINT chk_flash_sale_campaign_no CHECK (CHAR_LENGTH(TRIM(campaign_no)) BETWEEN 1 AND 40),
    CONSTRAINT chk_flash_sale_title CHECK (CHAR_LENGTH(TRIM(title)) BETWEEN 1 AND 200),
    CONSTRAINT chk_flash_sale_status CHECK (status IN ('draft', 'enabled', 'disabled', 'ended')),
    CONSTRAINT chk_flash_sale_price CHECK (price_cent > 0),
    CONSTRAINT chk_flash_sale_stock_limit CHECK (
        total_stock > 0 AND per_user_limit BETWEEN 1 AND total_stock
    ),
    CONSTRAINT chk_flash_sale_time CHECK (ends_at > starts_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE flash_sale_requests (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    campaign_id BIGINT UNSIGNED NOT NULL,
    buyer_id BIGINT UNSIGNED NOT NULL,
    idempotency_key VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
    reservation_token VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    quantity INT UNSIGNED NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'accepted',
    order_id BIGINT UNSIGNED NULL,
    failure_code VARCHAR(64) NULL,
    failure_reason VARCHAR(500) NULL,
    accepted_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_flash_sale_request_id (request_id),
    UNIQUE KEY uq_flash_sale_campaign_buyer_key (campaign_id, buyer_id, idempotency_key),
    UNIQUE KEY uq_flash_sale_reservation_token (reservation_token),
    UNIQUE KEY uq_flash_sale_request_order (order_id),
    KEY idx_flash_sale_requests_campaign_status (campaign_id, status, accepted_at, id),
    KEY idx_flash_sale_requests_buyer_page (buyer_id, accepted_at DESC, id DESC),
    CONSTRAINT fk_flash_sale_request_campaign FOREIGN KEY (campaign_id) REFERENCES flash_sale_campaigns (id),
    CONSTRAINT fk_flash_sale_request_order FOREIGN KEY (order_id) REFERENCES orders (id),
    CONSTRAINT chk_flash_sale_request_id CHECK (CHAR_LENGTH(TRIM(request_id)) BETWEEN 1 AND 64),
    CONSTRAINT chk_flash_sale_idempotency_key CHECK (
        CHAR_LENGTH(TRIM(idempotency_key)) BETWEEN 1 AND 128
    ),
    CONSTRAINT chk_flash_sale_reservation CHECK (CHAR_LENGTH(reservation_token) = 64),
    CONSTRAINT chk_flash_sale_request_quantity CHECK (quantity BETWEEN 1 AND 999),
    CONSTRAINT chk_flash_sale_request_status CHECK (
        status IN ('accepted', 'processing', 'order_created', 'failed', 'compensated', 'canceled', 'paid')
    ),
    CONSTRAINT chk_flash_sale_request_order_state CHECK (
        (status IN ('order_created', 'canceled', 'paid') AND order_id IS NOT NULL)
        OR (status IN ('accepted', 'processing', 'failed', 'compensated'))
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE flash_sale_reconciliation_anomalies (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    campaign_id BIGINT UNSIGNED NOT NULL,
    anomaly_key VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
    anomaly_type VARCHAR(32) NOT NULL,
    request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    order_id BIGINT UNSIGNED NULL,
    expected_value BIGINT NULL,
    actual_value BIGINT NULL,
    detail VARCHAR(1000) NOT NULL,
    evidence_json JSON NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'open',
    resolution VARCHAR(1000) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    resolved_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_flash_sale_reconciliation_key (campaign_id, anomaly_key),
    KEY idx_flash_sale_reconciliation_open (status, created_at, id),
    KEY idx_flash_sale_reconciliation_campaign (campaign_id, created_at, id),
    CONSTRAINT fk_flash_sale_reconciliation_campaign FOREIGN KEY (campaign_id) REFERENCES flash_sale_campaigns (id),
    CONSTRAINT fk_flash_sale_reconciliation_order FOREIGN KEY (order_id) REFERENCES orders (id),
    CONSTRAINT chk_flash_sale_reconciliation_key CHECK (CHAR_LENGTH(TRIM(anomaly_key)) BETWEEN 1 AND 128),
    CONSTRAINT chk_flash_sale_reconciliation_type CHECK (
        anomaly_type IN ('stock_mismatch', 'reservation_orphan', 'order_orphan', 'request_state_mismatch')
    ),
    CONSTRAINT chk_flash_sale_reconciliation_detail CHECK (
        CHAR_LENGTH(TRIM(detail)) BETWEEN 1 AND 1000
    ),
    CONSTRAINT chk_flash_sale_reconciliation_evidence CHECK (JSON_TYPE(evidence_json) = 'OBJECT'),
    CONSTRAINT chk_flash_sale_reconciliation_status CHECK (status IN ('open', 'resolved', 'ignored'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
