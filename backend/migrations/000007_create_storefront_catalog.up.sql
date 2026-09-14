CREATE TABLE shops (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    owner_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(120) NOT NULL,
    description VARCHAR(1000) NOT NULL DEFAULT '',
    avatar_url VARCHAR(1024) NOT NULL DEFAULT '',
    banner_url VARCHAR(1024) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'closed',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_shops_owner (owner_id),
    KEY idx_shops_public_page (status, created_at DESC, id DESC),
    CONSTRAINT chk_shops_name CHECK (CHAR_LENGTH(TRIM(name)) BETWEEN 1 AND 120),
    CONSTRAINT chk_shops_status CHECK (status IN ('open', 'closed', 'disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE categories (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    parent_id BIGINT UNSIGNED NULL,
    name VARCHAR(120) NOT NULL,
    slug VARCHAR(120) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    sort_order INT UNSIGNED NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_categories_slug (slug),
    KEY idx_categories_parent_page (parent_id, status, sort_order, id),
    CONSTRAINT fk_categories_parent FOREIGN KEY (parent_id) REFERENCES categories (id) ON DELETE SET NULL,
    CONSTRAINT chk_categories_name CHECK (CHAR_LENGTH(TRIM(name)) BETWEEN 1 AND 120),
    CONSTRAINT chk_categories_slug CHECK (CHAR_LENGTH(TRIM(slug)) BETWEEN 1 AND 120),
    CONSTRAINT chk_categories_status CHECK (status IN ('active', 'disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE products (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    shop_id BIGINT UNSIGNED NOT NULL,
    category_id BIGINT UNSIGNED NULL,
    name VARCHAR(200) NOT NULL,
    subtitle VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL,
    cover_url VARCHAR(1024) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    sales_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_products_shop_page (shop_id, status, created_at DESC, id DESC),
    KEY idx_products_public_page (status, created_at DESC, id DESC),
    KEY idx_products_category_page (category_id, status, created_at DESC, id DESC),
    CONSTRAINT fk_products_shop FOREIGN KEY (shop_id) REFERENCES shops (id),
    CONSTRAINT fk_products_category FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE SET NULL,
    CONSTRAINT chk_products_name CHECK (CHAR_LENGTH(TRIM(name)) BETWEEN 1 AND 200),
    CONSTRAINT chk_products_status CHECK (status IN ('draft', 'on_sale', 'off_sale', 'disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE product_images (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    product_id BIGINT UNSIGNED NOT NULL,
    storage_key VARCHAR(255) NOT NULL,
    image_url VARCHAR(1024) NOT NULL,
    mime_type VARCHAR(64) NOT NULL,
    byte_size BIGINT UNSIGNED NOT NULL,
    width INT UNSIGNED NOT NULL,
    height INT UNSIGNED NOT NULL,
    sort_order INT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_product_images_storage_key (storage_key),
    UNIQUE KEY uq_product_images_order (product_id, sort_order),
    KEY idx_product_images_product_order (product_id, sort_order, id),
    CONSTRAINT fk_product_images_product FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE CASCADE,
    CONSTRAINT chk_product_images_dimensions CHECK (byte_size > 0 AND width > 0 AND height > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE product_skus (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    product_id BIGINT UNSIGNED NOT NULL,
    sku_code VARCHAR(64) NOT NULL,
    specs_json JSON NOT NULL,
    price_cent BIGINT NOT NULL,
    original_price_cent BIGINT NULL,
    available_stock BIGINT NOT NULL DEFAULT 0,
    locked_stock BIGINT NOT NULL DEFAULT 0,
    sold_stock BIGINT NOT NULL DEFAULT 0,
    version BIGINT UNSIGNED NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'inactive',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_product_skus_code (sku_code),
    KEY idx_product_skus_product_sale (product_id, status, price_cent, id),
    KEY idx_product_skus_stock (status, available_stock, id),
    CONSTRAINT fk_product_skus_product FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE CASCADE,
    CONSTRAINT chk_product_skus_specs CHECK (JSON_TYPE(specs_json) = 'OBJECT'),
    CONSTRAINT chk_product_skus_price CHECK (
        price_cent > 0 AND (original_price_cent IS NULL OR original_price_cent >= price_cent)
    ),
    CONSTRAINT chk_product_skus_stock CHECK (
        available_stock >= 0 AND locked_stock >= 0 AND sold_stock >= 0
    ),
    CONSTRAINT chk_product_skus_status CHECK (status IN ('active', 'inactive', 'disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
