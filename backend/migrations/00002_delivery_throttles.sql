-- +goose Up

-- 429 replies are counted apart from attempts (D-RETRY): waiting for a
-- provider's rate limit is not a failure of the message, but it still needs
-- an upper bound so a row cannot be throttled forever.
ALTER TABLE deliveries ADD COLUMN throttles integer NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE deliveries DROP COLUMN throttles;
