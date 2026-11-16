-- Forty events spread over the last ten days, deterministic per row.
INSERT INTO events (account_id, name, at)
SELECT 1 + (g % 8),
       (ARRAY['login', 'report.run', 'export', 'invite.sent'])[1 + (g % 4)],
       now() - make_interval(hours => g * 6)
FROM generate_series(1, 40) AS g;
