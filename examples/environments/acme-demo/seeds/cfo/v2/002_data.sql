-- cfo@v2 data: eight customers, twelve orders, invoices for the paid ones.
INSERT INTO customers (name, segment, country) VALUES
  ('Fernhollow Traders', 'enterprise', 'US'),
  ('Blue Harbor Logistics', 'mid-market', 'CA'),
  ('Kestrel Analytics', 'startup', 'GB'),
  ('Sunridge Retail', 'mid-market', 'US'),
  ('Aurora Health', 'enterprise', 'DE'),
  ('Pinecrest Foods', 'smb', 'US'),
  ('Meridian Energy', 'enterprise', 'AU'),
  ('Cobalt Studios', 'startup', 'US');

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
  (4,  180000, 'paid',    '2026-09-10 15:55+00'),
  (7, 5400000, 'paid',    '2026-09-11 07:30+00'),
  (8,   12000, 'open',    '2026-09-14 19:10+00');

INSERT INTO invoices (order_id, number, issued_at, due_at, paid_at) VALUES
  (1,  'INV-2026-0701', '2026-07-02', '2026-08-01', '2026-07-28'),
  (2,  'INV-2026-0702', '2026-07-19', '2026-08-18', '2026-08-15'),
  (3,  'INV-2026-0703', '2026-07-21', '2026-08-20', '2026-08-19'),
  (5,  'INV-2026-0801', '2026-08-05', '2026-09-04', '2026-09-01'),
  (6,  'INV-2026-0802', '2026-08-12', '2026-09-11', '2026-09-09'),
  (8,  'INV-2026-0803', '2026-08-28', '2026-09-27', '2026-09-12'),
  (10, 'INV-2026-0901', '2026-09-10', '2026-10-10', NULL),
  (11, 'INV-2026-0902', '2026-09-11', '2026-10-11', NULL);
