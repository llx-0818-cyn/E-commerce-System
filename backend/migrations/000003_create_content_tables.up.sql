CREATE TABLE posts (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    author_id BIGINT UNSIGNED NOT NULL,
    title VARCHAR(200) NOT NULL,
    body TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'published',
    likes_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
    favorites_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
    comments_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
    popularity DECIMAL(20, 6) NOT NULL DEFAULT 0,
    published_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_posts_public_page (status, published_at DESC, id DESC),
    KEY idx_posts_author_page (author_id, status, published_at DESC, id DESC),
    KEY idx_posts_popularity_page (status, popularity DESC, published_at DESC, id DESC),
    KEY idx_posts_deleted_cleanup (deleted_at, id),
    CONSTRAINT chk_posts_status CHECK (status IN ('draft', 'review', 'published', 'hidden', 'deleted')),
    CONSTRAINT chk_posts_visibility CHECK (
        (status = 'published' AND published_at IS NOT NULL AND deleted_at IS NULL)
        OR (status IN ('draft', 'review', 'hidden') AND deleted_at IS NULL)
        OR (status = 'deleted' AND deleted_at IS NOT NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE post_images (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    owner_id BIGINT UNSIGNED NOT NULL,
    post_id BIGINT UNSIGNED NULL,
    storage_key VARCHAR(255) NOT NULL,
    image_url VARCHAR(1024) NOT NULL,
    mime_type VARCHAR(64) NOT NULL,
    byte_size BIGINT UNSIGNED NOT NULL,
    width INT UNSIGNED NOT NULL,
    height INT UNSIGNED NOT NULL,
    sort_order INT UNSIGNED NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    attached_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_post_images_storage_key (storage_key),
    UNIQUE KEY uq_post_images_order (post_id, sort_order),
    KEY idx_post_images_post_order (post_id, sort_order, id),
    KEY idx_post_images_owner_unattached (owner_id, post_id, created_at, id),
    CONSTRAINT fk_post_images_post FOREIGN KEY (post_id) REFERENCES posts (id) ON DELETE CASCADE,
    CONSTRAINT chk_post_images_dimensions CHECK (byte_size > 0 AND width > 0 AND height > 0),
    CONSTRAINT chk_post_images_attachment CHECK (
        (post_id IS NULL AND sort_order IS NULL AND attached_at IS NULL)
        OR (post_id IS NOT NULL AND sort_order IS NOT NULL AND attached_at IS NOT NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE post_tags (
    post_id BIGINT UNSIGNED NOT NULL,
    tag_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (post_id, tag_id),
    KEY idx_post_tags_tag_post (tag_id, post_id),
    CONSTRAINT fk_post_tags_post FOREIGN KEY (post_id) REFERENCES posts (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
