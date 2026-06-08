-- Migration 068: Separate window seconds for anonymous and authenticated traffic
ALTER TABLE system.rate_limits
ADD COLUMN IF NOT EXISTS window_seconds_anon INTEGER DEFAULT 1,
ADD COLUMN IF NOT EXISTS window_seconds_auth INTEGER DEFAULT 1;

COMMENT ON COLUMN system.rate_limits.window_seconds_anon IS 'Time window in seconds for anonymous users';
COMMENT ON COLUMN system.rate_limits.window_seconds_auth IS 'Time window in seconds for authenticated users';
