-- auto_invest_events: basket_id and order_id are legacy FK columns the service never populates.
-- Drop the NOT NULL constraints and FK references so event inserts don't fail.
ALTER TABLE auto_invest_events
    DROP CONSTRAINT IF EXISTS auto_invest_events_basket_id_fkey,
    DROP CONSTRAINT IF EXISTS auto_invest_events_order_id_fkey,
    ALTER COLUMN basket_id DROP NOT NULL,
    ALTER COLUMN order_id  DROP NOT NULL;

-- auto_invest_settings: basket_id is not used by the strategy-based service.
ALTER TABLE auto_invest_settings
    DROP CONSTRAINT IF EXISTS auto_invest_settings_basket_id_fkey,
    ALTER COLUMN basket_id DROP NOT NULL;
