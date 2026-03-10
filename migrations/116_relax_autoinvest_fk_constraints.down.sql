ALTER TABLE auto_invest_events
    ALTER COLUMN basket_id SET NOT NULL,
    ALTER COLUMN order_id  SET NOT NULL,
    ADD CONSTRAINT auto_invest_events_basket_id_fkey FOREIGN KEY (basket_id) REFERENCES baskets(id),
    ADD CONSTRAINT auto_invest_events_order_id_fkey  FOREIGN KEY (order_id)  REFERENCES orders(id);

ALTER TABLE auto_invest_settings
    ALTER COLUMN basket_id SET NOT NULL,
    ADD CONSTRAINT auto_invest_settings_basket_id_fkey FOREIGN KEY (basket_id) REFERENCES baskets(id);
