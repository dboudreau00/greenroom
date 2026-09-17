-- cfo@v2 schema: customers, orders and invoices.
DROP VIEW IF EXISTS quarter_close;
DROP TABLE IF EXISTS invoices CASCADE;
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

CREATE TABLE invoices (
  id          serial PRIMARY KEY,
  order_id    integer NOT NULL REFERENCES orders(id),
  number      text NOT NULL UNIQUE,
  issued_at   date NOT NULL,
  due_at      date NOT NULL,
  paid_at     date
);

CREATE INDEX orders_customer_idx ON orders(customer_id);
CREATE INDEX invoices_order_idx ON invoices(order_id);
