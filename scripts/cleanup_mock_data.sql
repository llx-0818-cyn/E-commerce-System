-- Remove only the repeatable content-commerce seed batch.
-- Existing rows without the codexcommerce_ prefix are preserved.

SET NAMES utf8mb4;
START TRANSACTION;
SET FOREIGN_KEY_CHECKS = 0;

DROP TEMPORARY TABLE IF EXISTS seed_accounts;
DROP TEMPORARY TABLE IF EXISTS seed_shops;
DROP TEMPORARY TABLE IF EXISTS seed_products;
DROP TEMPORARY TABLE IF EXISTS seed_skus;
DROP TEMPORARY TABLE IF EXISTS seed_posts;
DROP TEMPORARY TABLE IF EXISTS seed_orders;
DROP TEMPORARY TABLE IF EXISTS seed_campaigns;
DROP TEMPORARY TABLE IF EXISTS seed_payments;

CREATE TEMPORARY TABLE seed_accounts (id BIGINT UNSIGNED PRIMARY KEY);
INSERT INTO seed_accounts SELECT id FROM accounts WHERE username LIKE 'codexcommerce\_%';

CREATE TEMPORARY TABLE seed_shops (id BIGINT UNSIGNED PRIMARY KEY);
INSERT INTO seed_shops SELECT id FROM shops WHERE owner_id IN (SELECT id FROM seed_accounts);

CREATE TEMPORARY TABLE seed_products (id BIGINT UNSIGNED PRIMARY KEY);
INSERT INTO seed_products SELECT id FROM products WHERE shop_id IN (SELECT id FROM seed_shops);

CREATE TEMPORARY TABLE seed_skus (id BIGINT UNSIGNED PRIMARY KEY);
INSERT INTO seed_skus SELECT id FROM product_skus WHERE product_id IN (SELECT id FROM seed_products) OR sku_code LIKE 'CC-SEED-%';

CREATE TEMPORARY TABLE seed_posts (id BIGINT UNSIGNED PRIMARY KEY);
INSERT INTO seed_posts SELECT id FROM posts WHERE author_id IN (SELECT id FROM seed_accounts) OR title LIKE '[CC-SEED]%';

CREATE TEMPORARY TABLE seed_orders (id BIGINT UNSIGNED PRIMARY KEY);
INSERT INTO seed_orders
SELECT id FROM orders
WHERE buyer_id IN (SELECT id FROM seed_accounts)
   OR shop_id IN (SELECT id FROM seed_shops)
   OR order_no LIKE 'CC-SEED-%';

CREATE TEMPORARY TABLE seed_campaigns (id BIGINT UNSIGNED PRIMARY KEY);
INSERT INTO seed_campaigns SELECT id FROM flash_sale_campaigns WHERE sku_id IN (SELECT id FROM seed_skus) OR campaign_no LIKE 'FS-SEED-%';

CREATE TEMPORARY TABLE seed_payments (id BIGINT UNSIGNED PRIMARY KEY);
INSERT INTO seed_payments SELECT id FROM payment_records WHERE order_id IN (SELECT id FROM seed_orders);

DELETE FROM payment_anomalies WHERE order_id IN (SELECT id FROM seed_orders) OR payment_id IN (SELECT id FROM seed_payments);
DELETE FROM payment_callback_events WHERE payment_id IN (SELECT id FROM seed_payments);
DELETE FROM payment_records WHERE order_id IN (SELECT id FROM seed_orders);

DELETE FROM flash_sale_reconciliation_anomalies WHERE campaign_id IN (SELECT id FROM seed_campaigns) OR order_id IN (SELECT id FROM seed_orders);
DELETE FROM flash_sale_requests WHERE campaign_id IN (SELECT id FROM seed_campaigns) OR buyer_id IN (SELECT id FROM seed_accounts) OR order_id IN (SELECT id FROM seed_orders);
DELETE FROM flash_sale_campaigns WHERE id IN (SELECT id FROM seed_campaigns);

DELETE FROM inventory_ledger WHERE order_id IN (SELECT id FROM seed_orders) OR sku_id IN (SELECT id FROM seed_skus);
DELETE FROM order_items WHERE order_id IN (SELECT id FROM seed_orders) OR sku_id IN (SELECT id FROM seed_skus);
DELETE FROM orders WHERE id IN (SELECT id FROM seed_orders);
DELETE FROM order_submissions WHERE buyer_id IN (SELECT id FROM seed_accounts) OR idempotency_key LIKE 'cc-seed-%';

DELETE FROM cart_items WHERE account_id IN (SELECT id FROM seed_accounts) OR sku_id IN (SELECT id FROM seed_skus);
DELETE FROM addresses WHERE account_id IN (SELECT id FROM seed_accounts);

DELETE FROM post_likes WHERE account_id IN (SELECT id FROM seed_accounts) OR post_id IN (SELECT id FROM seed_posts);
DELETE FROM post_favorites WHERE account_id IN (SELECT id FROM seed_accounts) OR post_id IN (SELECT id FROM seed_posts);
DELETE FROM post_comments WHERE author_id IN (SELECT id FROM seed_accounts) OR post_id IN (SELECT id FROM seed_posts);
DELETE FROM post_products WHERE post_id IN (SELECT id FROM seed_posts) OR product_id IN (SELECT id FROM seed_products);
DELETE FROM post_images WHERE owner_id IN (SELECT id FROM seed_accounts) OR post_id IN (SELECT id FROM seed_posts) OR storage_key LIKE 'codexcommerce/%';
DELETE FROM post_tags WHERE post_id IN (SELECT id FROM seed_posts) OR tag_id IN (SELECT id FROM tags WHERE name LIKE 'codexcommerce\_%');
DELETE FROM posts WHERE id IN (SELECT id FROM seed_posts);
DELETE FROM tags WHERE name LIKE 'codexcommerce\_%';

DELETE FROM notifications_v2 WHERE recipient_id IN (SELECT id FROM seed_accounts);
DELETE FROM notifications_v2 WHERE sender_id IN (SELECT id FROM seed_accounts);
DELETE FROM socials WHERE follower_id IN (SELECT id FROM seed_accounts);
DELETE FROM socials WHERE vlogger_id IN (SELECT id FROM seed_accounts);

DELETE FROM product_images WHERE product_id IN (SELECT id FROM seed_products) OR storage_key LIKE 'codexcommerce/%';
DELETE FROM product_skus WHERE id IN (SELECT id FROM seed_skus);
DELETE FROM products WHERE id IN (SELECT id FROM seed_products);
DELETE FROM shops WHERE id IN (SELECT id FROM seed_shops);
DELETE FROM categories WHERE slug LIKE 'codexcommerce-%';
DELETE FROM accounts WHERE id IN (SELECT id FROM seed_accounts);

SET FOREIGN_KEY_CHECKS = 1;
COMMIT;

SELECT 'remaining_seed_accounts' AS entity, COUNT(*) AS row_count
FROM accounts WHERE username LIKE 'codexcommerce\_%'
UNION ALL
SELECT 'remaining_seed_products', COUNT(*) FROM product_skus WHERE sku_code LIKE 'CC-SEED-%'
UNION ALL
SELECT 'remaining_seed_campaigns', COUNT(*) FROM flash_sale_campaigns WHERE campaign_no LIKE 'FS-SEED-%';
