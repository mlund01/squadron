ALTER TABLE turn_costs ALTER COLUMN task_id DROP NOT NULL;
ALTER TABLE turn_costs ALTER COLUMN session_id DROP NOT NULL;
ALTER TABLE turn_costs DROP CONSTRAINT IF EXISTS turn_costs_task_id_fkey;
ALTER TABLE turn_costs DROP CONSTRAINT IF EXISTS turn_costs_session_id_fkey;
