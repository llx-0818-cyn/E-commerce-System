-- tags 是旧视频与新图文在并行迁移期共享的词典表。IF NOT EXISTS 允许已有
-- GORM 表无损接管；从本版本开始，tags 的 Schema 事实源归版本化 SQL，
-- LegacyAutoMigrate 不再包含 video.Tag。
CREATE TABLE IF NOT EXISTS tags (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name VARCHAR(100) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_tags_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
