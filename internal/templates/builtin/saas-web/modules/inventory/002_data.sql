INSERT INTO products (sku, name, reorder_point) VALUES
  ('TRL-100', 'Trail helmet',       40),
  ('TRL-110', 'Trail gloves',       60),
  ('CMT-200', 'Commuter lights',    50),
  ('CMT-210', 'Commuter lock',      30),
  ('RDE-300', 'Road jersey',        45),
  ('RDE-310', 'Road bottle cage',   80),
  ('KID-400', 'Kids balance bike',  12),
  ('KID-410', 'Kids helmet',        25),
  ('SVC-500', 'Tune-up kit',        20),
  ('SVC-510', 'Inner tube 700c',   150);

INSERT INTO warehouses (code, city) VALUES ('NORTH', 'Halden'), ('SOUTH', 'Tidewell');

-- Every product sits in both warehouses at its reorder point, so the total
-- is twice the point; the kids balance bike is the one item below it.
INSERT INTO stock (product_id, warehouse_id, units)
SELECT p.id, w.id, CASE WHEN p.sku = 'KID-400' THEN 4 ELSE p.reorder_point END
FROM products p CROSS JOIN warehouses w;
