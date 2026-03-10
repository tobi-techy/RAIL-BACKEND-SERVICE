-- Handle any events with NULL basket_id or order_id before adding NOT NULL constraints
-- Delete orphaned events that have no basket or order (these are incomplete/failed events)
DELETE FROM auto_invest_events WHERE basket_id IS NULL OR order_id IS NULL;

ALTER TABLE auto_invest_events
    ALTER COLUMN basket_id SET NOT NULL,
    ALTER COLUMN order_id  SET NOT NULL,
    ADD CONSTRAINT auto_invest_events_basket_id_fkey FOREIGN KEY (basket_id) REFERENCES baskets(id),
    ADD CONSTRAINT auto_invest_events_order_id_fkey  FOREIGN KEY (order_id)  REFERENCES orders(id);

-- Handle any settings with NULL basket_id before adding NOT NULL constraint
-- Delete settings that have no basket (these are incomplete/invalid settings)
DELETE FROM auto_invest_settings WHERE basket_id IS NULL;

ALTER TABLE auto_invest_settings
    ALTER COLUMN basket_id SET NOT NULL,
    ADD CONSTRAINT auto_invest_settings_basket_id_fkey FOREIGN KEY (basket_id) REFERENCES baskets(id);
