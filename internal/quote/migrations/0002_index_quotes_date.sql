-- Reads by session (GET /sessions/{date}/quotes) and the latest session (MAX(date)).
-- The primary key (ticker, date) covers every read by ticker.
CREATE INDEX IF NOT EXISTS idx_quotes_date ON quotes (date);
