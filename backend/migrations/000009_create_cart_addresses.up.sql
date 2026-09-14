CREATE TABLE cart_items (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    account_id BIGINT UNSIGNED NOT NULL,
    sku_id BIGINT UNSIGNED NOT NULL,
    quantity INT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_cart_items_account_sku (account_id, sku_id),
    KEY idx_cart_items_account_page (account_id, updated_at DESC, id DESC),
    KEY idx_cart_items_sku_account (sku_id, account_id),
    CONSTRAINT fk_cart_items_sku FOREIGN KEY (sku_id) REFERENCES product_skus (id) ON DELETE CASCADE,
    CONSTRAINT chk_cart_items_quantity CHECK (quantity BETWEEN 1 AND 999)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE addresses (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    account_id BIGINT UNSIGNED NOT NULL,
    recipient_name VARCHAR(100) NOT NULL,
    phone VARCHAR(32) NOT NULL,
    province VARCHAR(100) NOT NULL,
    city VARCHAR(100) NOT NULL,
    district VARCHAR(100) NOT NULL DEFAULT '',
    detail VARCHAR(500) NOT NULL,
    postal_code VARCHAR(32) NOT NULL DEFAULT '',
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    default_account_id BIGINT UNSIGNED GENERATED ALWAYS AS (
        CASE WHEN is_default = TRUE THEN account_id ELSE NULL END
    ) STORED,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_addresses_single_default (default_account_id),
    KEY idx_addresses_account_page (account_id, updated_at DESC, id DESC),
    CONSTRAINT chk_addresses_recipient CHECK (CHAR_LENGTH(TRIM(recipient_name)) BETWEEN 1 AND 100),
    CONSTRAINT chk_addresses_phone CHECK (CHAR_LENGTH(TRIM(phone)) BETWEEN 1 AND 32),
    CONSTRAINT chk_addresses_region CHECK (
        CHAR_LENGTH(TRIM(province)) BETWEEN 1 AND 100
        AND CHAR_LENGTH(TRIM(city)) BETWEEN 1 AND 100
        AND CHAR_LENGTH(TRIM(detail)) BETWEEN 1 AND 500
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
