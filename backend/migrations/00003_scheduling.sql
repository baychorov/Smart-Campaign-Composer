-- +goose Up

-- Scheduled sending (PLAN step 15a). For a draft, scheduled_at is the send
-- time the model suggested from the brief; after approval it is the time
-- the marketer chose. Deliveries of a scheduled campaign are written at
-- approval with next_attempt_at = scheduled_at, so the worker's claim query
-- holds them back until then.
ALTER TABLE campaigns ADD COLUMN scheduled_at timestamptz;

ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_status_check
    CHECK (status IN ('draft', 'approved', 'scheduled', 'sending', 'completed', 'cancelled'));

-- +goose Down

DELETE FROM campaigns WHERE status IN ('scheduled', 'cancelled');
ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_status_check
    CHECK (status IN ('draft', 'approved', 'sending', 'completed'));
ALTER TABLE campaigns DROP COLUMN scheduled_at;
