-- cfo@v1 schema. Runs after the reset strategy, so the database is empty.
DROP TABLE IF EXISTS orders CASCADE;
DROP TABLE IF EXISTS customers CASCADE;

CREATE TABLE customers (
  id          serial PRIMARY KEY,
  name        text NOT NULL,
  segment     text NOT NULL,
  country     text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE orders (
  id           serial PRIMARY KEY,
  customer_id  integer NOT NULL REFERENCES customers(id),
  amount_cents bigint NOT NULL,
  currency     text NOT NULL DEFAULT 'USD',
  status       text NOT NULL,
  placed_at    timestamptz NOT NULL
);

CREATE INDEX orders_customer_idx ON orders(customer_id);
