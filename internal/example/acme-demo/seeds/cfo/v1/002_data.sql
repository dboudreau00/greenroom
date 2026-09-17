-- cfo@v1 data: six customers, ten orders.
INSERT INTO customers (name, segment, country) VALUES
  ('Northwind Traders', 'enterprise', 'US'),
  ('Blue Harbor Logistics', 'mid-market', 'CA'),
  ('Kestrel Analytics', 'startup', 'GB'),
  ('Sunridge Retail', 'mid-market', 'US'),
  ('Aurora Health', 'enterprise', 'DE'),
  ('Pinecrest Foods', 'smb', 'US');

INSERT INTO orders (customer_id, amount_cents, status, placed_at) VALUES
  (1, 1250000, 'paid',    '2026-07-02 10:15+00'),
  (1,  980000, 'paid',    '2026-07-19 14:02+00'),
  (2,  410000, 'paid',    '2026-07-21 09:40+00'),
  (3,   75000, 'open',    '2026-08-01 16:30+00'),
  (4,  215000, 'paid',    '2026-08-05 11:11+00'),
  (5, 3100000, 'paid',    '2026-08-12 08:00+00'),
  (5,  460000, 'refund',  '2026-08-20 13:45+00'),
  (6,   39000, 'paid',    '2026-08-28 17:20+00'),
  (2,  520000, 'open',    '2026-09-03 10:05+00'),
  (4,  180000, 'paid',    '2026-09-10 15:55+00');
