-- retail-ops@v1 data.
INSERT INTO stores (name, city, region) VALUES
  ('Downtown', 'Seattle', 'west'),
  ('Riverside', 'Austin', 'south'),
  ('Harbor', 'Boston', 'east');

INSERT INTO products (sku, name, price_cents) VALUES
  ('SKU-1001', 'Trail Jacket', 14900),
  ('SKU-1002', 'Insulated Bottle', 3200),
  ('SKU-1003', 'Day Pack 22L', 8900),
  ('SKU-1004', 'Merino Socks', 1800);

INSERT INTO inventory (store_id, product_id, on_hand, reorder_point) VALUES
  (1, 1, 12, 10), (1, 2, 4, 15), (1, 3, 20, 8), (1, 4, 60, 40),
  (2, 1,  3, 10), (2, 2, 30, 15), (2, 3,  5, 8), (2, 4, 25, 40),
  (3, 1, 18, 10), (3, 2, 16, 15), (3, 3,  9, 8), (3, 4, 44, 40);

INSERT INTO customers (name, segment, country) VALUES
  ('Walk-in', 'consumer', 'US'),
  ('Cascade Outfitters', 'wholesale', 'US'),
  ('Lakeside School', 'education', 'US');

INSERT INTO orders (customer_id, store_id, amount_cents, status, placed_at) VALUES
  (1, 1, 14900, 'paid', '2026-09-01 10:00+00'),
  (1, 1,  5000, 'paid', '2026-09-01 12:30+00'),
  (2, 2, 89000, 'paid', '2026-09-03 09:15+00'),
  (1, 3,  1800, 'paid', '2026-09-04 16:45+00'),
  (3, 3, 32000, 'open', '2026-09-08 11:20+00'),
  (1, 2,  3200, 'paid', '2026-09-09 14:05+00'),
  (2, 1, 44700, 'open', '2026-09-12 08:50+00'),
  (1, 3,  8900, 'paid', '2026-09-14 17:35+00');
