ALTER TABLE human_input_requests ADD COLUMN deadline_at TEXT;
ALTER TABLE human_input_requests ADD COLUMN expired_at TEXT;

CREATE INDEX IF NOT EXISTS idx_human_input_deadline
    ON human_input_requests(state, deadline_at);
