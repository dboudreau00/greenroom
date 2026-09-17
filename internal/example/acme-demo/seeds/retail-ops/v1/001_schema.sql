-- retail-ops@v1 schema: stores, products, inventory, customers, orders.
DROP TABLE IF EXISTS orders CASCADE;
DROP TABLE IF EXISTS inventory CASCADE;
DROP TABLE IF EXISTS products CASCADE;
DROP TABLE IF EXISTS stores CASCADE;
DROP TABLE IF EXISTS customers CASCADE;

CREATE TABLE stores (
  id      serial PRIMARY KEY,
  name    text NOT NULL,
  city    text NOT NULL,
  region  text NOT NULL
);

CREATE TABLE products (
  id    serial PRIMARY KEY,
  sku   text NOT NULL UNIQUE,
  name  text NOT NULL,
  price_cents integer NOT NULL
);

CREATE TABLE inventory (
  store_id      integer NOT NULL REFERENCES stores(id),
  product_id    integer NOT NULL REFERENCES products(id),
  on_hand       integer NOT NULL,
  reorder_point integer NOT NULL,
  PRIMARY KEY (store_id, product_id)
);

CREATE TABLE customers (
  id      serial PRIMARY KEY,
  name    text NOT NULL,
  segment text NOT NULL,
  country text NOT NULL
);

CREATE TABLE orders (
  id           serial PRIMARY KEY,
  customer_id  integer NOT NULL REFERENCES customers(id),
  store_id     integer NOT NULL REFERENCES stores(id),
  amount_cents bigint NOT NULL,
  status       text NOT NULL,
  placed_at    timestamptz NOT NULL
);
