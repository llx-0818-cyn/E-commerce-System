CREATE TABLE order_submissions (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    buyer_id BIGINT UNSIGNED NOT NULL,
    idempotency_key VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
    request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    grand_total_cent BIGINT NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_order_submissions_buyer_key (buyer_id, idempotency_key),
    UNIQUE KEY uq_order_submissions_id_buyer (id, buyer_id),
    KEY idx_order_submissions_buyer_page (buyer_id, created_at DESC, id DESC),
    CONSTRAINT chk_order_submissions_key CHECK (
        CHAR_LENGTH(TRIM(idempotency_key)) BETWEEN 1 AND 128
    ),
    CONSTRAINT chk_order_submissions_fingerprint CHECK (
        CHAR_LENGTH(request_fingerprint) = 64
    ),
    CONSTRAINT chk_order_submissions_total CHECK (grand_total_cent > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE orders (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    submission_id BIGINT UNSIGNED NOT NULL,
    order_no VARCHAR(40) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    buyer_id BIGINT UNSIGNED NOT NULL,
    shop_id BIGINT UNSIGNED NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending_payment',
    total_cent BIGINT NOT NULL,
    address_snapshot_json JSON NOT NULL,
    expire_at DATETIME(6) NOT NULL,
    paid_at DATETIME(6) NULL,
    shipped_at DATETIME(6) NULL,
    completed_at DATETIME(6) NULL,
    canceled_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_orders_order_no (order_no),
    UNIQUE KEY uq_orders_submission_shop (submission_id, shop_id),
    KEY idx_orders_buyer_page (buyer_id, created_at DESC, id DESC),
    KEY idx_orders_buyer_status_page (buyer_id, status, created_at DESC, id DESC),
    KEY idx_orders_shop_status_page (shop_id, status, created_at DESC, id DESC),
    KEY idx_orders_expire_scan (status, expire_at, id),
    CONSTRAINT fk_orders_submission_buyer FOREIGN KEY (submission_id, buyer_id)
        REFERENCES order_submissions (id, buyer_id),
    CONSTRAINT fk_orders_shop FOREIGN KEY (shop_id) REFERENCES shops (id),
    CONSTRAINT chk_orders_order_no CHECK (CHAR_LENGTH(TRIM(order_no)) BETWEEN 1 AND 40),
    CONSTRAINT chk_orders_status CHECK (
        status IN ('pending_payment', 'paid', 'shipped', 'completed', 'canceled')
    ),
    CONSTRAINT chk_orders_total CHECK (total_cent > 0),
    CONSTRAINT chk_orders_address_snapshot CHECK (JSON_TYPE(address_snapshot_json) = 'OBJECT'),
    CONSTRAINT chk_orders_expire_at CHECK (expire_at > created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE order_items (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    order_id BIGINT UNSIGNED NOT NULL,
    product_id BIGINT UNSIGNED NOT NULL,
    product_name VARCHAR(200) NOT NULL,
    cover_url VARCHAR(1024) NOT NULL DEFAULT '',
    sku_id BIGINT UNSIGNED NOT NULL,
    sku_code VARCHAR(64) NOT NULL,
    specs_json JSON NOT NULL,
    unit_price_cent BIGINT NOT NULL,
    quantity INT UNSIGNED NOT NULL,
    subtotal_cent BIGINT NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_order_items_order_sku (order_id, sku_id),
    KEY idx_order_items_sku_order (sku_id, order_id),
    CONSTRAINT fk_order_items_order FOREIGN KEY (order_id) REFERENCES orders (id),
    CONSTRAINT chk_order_items_product_name CHECK (
        CHAR_LENGTH(TRIM(product_name)) BETWEEN 1 AND 200
    ),
    CONSTRAINT chk_order_items_sku_code CHECK (CHAR_LENGTH(TRIM(sku_code)) BETWEEN 1 AND 64),
    CONSTRAINT chk_order_items_specs CHECK (JSON_TYPE(specs_json) = 'OBJECT'),
    CONSTRAINT chk_order_items_amount CHECK (
        unit_price_cent > 0
        AND quantity BETWEEN 1 AND 999
        AND subtotal_cent = unit_price_cent * quantity
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE inventory_ledger (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    event_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    order_id BIGINT UNSIGNED NOT NULL,
    sku_id BIGINT UNSIGNED NOT NULL,
    movement_type VARCHAR(16) NOT NULL,
    quantity BIGINT NOT NULL,
    available_delta BIGINT NOT NULL,
    locked_delta BIGINT NOT NULL,
    sold_delta BIGINT NOT NULL,
    sku_version BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_inventory_ledger_event (event_id),
    UNIQUE KEY uq_inventory_ledger_order_sku_movement (order_id, sku_id, movement_type),
    KEY idx_inventory_ledger_sku_page (sku_id, created_at DESC, id DESC),
    KEY idx_inventory_ledger_order_page (order_id, created_at, id),
    CONSTRAINT fk_inventory_ledger_order FOREIGN KEY (order_id) REFERENCES orders (id),
    CONSTRAINT chk_inventory_ledger_event CHECK (CHAR_LENGTH(TRIM(event_id)) BETWEEN 1 AND 64),
    CONSTRAINT chk_inventory_ledger_movement CHECK (
        (movement_type = 'lock'
            AND quantity > 0
            AND available_delta = -quantity
            AND locked_delta = quantity
            AND sold_delta = 0)
        OR (movement_type = 'release'
            AND quantity > 0
            AND available_delta = quantity
            AND locked_delta = -quantity
            AND sold_delta = 0)
        OR (movement_type = 'sell'
            AND quantity > 0
            AND available_delta = 0
            AND locked_delta = -quantity
            AND sold_delta = quantity)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
