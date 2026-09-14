ALTER TABLE cart_items
    DROP CHECK chk_cart_items_observed_price,
    DROP COLUMN observed_price_cent;
