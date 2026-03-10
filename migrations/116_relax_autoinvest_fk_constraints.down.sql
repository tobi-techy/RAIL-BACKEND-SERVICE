-- Safety check: refuse rollback if any events have NULL basket_id or order_id
DO $$
DECLARE
    null_basket_count INTEGER;
    null_order_count  INTEGER;
BEGIN
    SELECT COUNT(*) INTO null_basket_count FROM auto_invest_events WHERE basket_id IS NULL;
    SELECT COUNT(*) INTO null_order_count  FROM auto_invest_events WHERE order_id  IS NULL;
    IF null_basket_count > 0 OR null_order_count > 0 THEN
        RAISE EXCEPTION 'Cannot rollback: % event(s) have NULL basket_id and % event(s) have NULL order_id. Complete or delete these correlation-based events before rolling back.', null_basket_count, null_order_count;
    END IF;
END $$;

ALTER TABLE auto_invest_events
    ALTER COLUMN basket_id SET NOT NULL,
    ALTER COLUMN order_id  SET NOT NULL,
    ADD CONSTRAINT auto_invest_events_basket_id_fkey FOREIGN KEY (basket_id) REFERENCES baskets(id),
    ADD CONSTRAINT auto_invest_events_order_id_fkey  FOREIGN KEY (order_id)  REFERENCES orders(id);

ALTER TABLE auto_invest_settings
    ALTER COLUMN basket_id SET NOT NULL,
    ADD CONSTRAINT auto_invest_settings_basket_id_fkey FOREIGN KEY (basket_id) REFERENCES baskets(id);
