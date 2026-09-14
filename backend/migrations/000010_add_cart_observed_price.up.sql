ALTER TABLE cart_items
    ADD COLUMN observed_price_cent BIGINT NOT NULL DEFAULT 0 AFTER quantity,
    ADD CONSTRAINT chk_cart_items_observed_price CHECK (observed_price_cent >= 0);
