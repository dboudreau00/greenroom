-- Orders: twenty orders, one stuck in fulfilment for three days.
CREATE TABLE orders (
  id          serial PRIMARY KEY,
  customer    text        NOT NULL,
  product_id  int         NOT NULL REFERENCES products(id),
  quantity    int         NOT NULL,
  status      text        NOT NULL,
  placed_at   timestamptz NOT NULL
);

INSERT INTO orders (customer, product_id, quantity, status, placed_at)
SELECT 'customer-' || lpad(g::text, 3, '0'), 1 + (g % 12), 1 + (g % 3),
       CASE WHEN g <= 15 THEN 'delivered' ELSE 'shipped' END,
       now() - make_interval(hours => g * 9)
FROM generate_series(1, 19) AS g;

INSERT INTO orders (customer, product_id, quantity, status, placed_at)
VALUES ('customer-020', 6, 1, 'fulfilling', now() - interval '3 days');
