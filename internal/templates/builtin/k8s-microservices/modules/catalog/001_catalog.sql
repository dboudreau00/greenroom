-- Catalog: three categories, twelve products.
CREATE TABLE categories (
  id   int PRIMARY KEY,
  name text NOT NULL
);

CREATE TABLE products (
  id          serial PRIMARY KEY,
  category_id int  NOT NULL REFERENCES categories(id),
  sku         text NOT NULL UNIQUE,
  name        text NOT NULL,
  price_cents int  NOT NULL
);

INSERT INTO categories (id, name) VALUES (1, 'Coffee'), (2, 'Brewing gear'), (3, 'Subscriptions');

INSERT INTO products (category_id, sku, name, price_cents) VALUES
  (1, 'CF-ETH', 'Yirgacheffe whole bean',     1800),
  (1, 'CF-COL', 'Huila single origin',        1650),
  (1, 'CF-BLD', 'House espresso blend',       1500),
  (1, 'CF-DCF', 'Swiss water decaf',          1700),
  (2, 'GR-V60', 'Pour-over dripper',          2900),
  (2, 'GR-GRN', 'Burr grinder',              14900),
  (2, 'GR-KTL', 'Gooseneck kettle',           6900),
  (2, 'GR-SCL', 'Brew scale',                 4500),
  (2, 'GR-FLT', 'Paper filters, 100 pack',     700),
  (3, 'SB-MON', 'Monthly roaster pick',       2200),
  (3, 'SB-BIW', 'Biweekly espresso',          3900),
  (3, 'SB-OFC', 'Office plan, 5 kg a month', 18500);
