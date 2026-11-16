-- Payments: one payment per order, one declined card, one refund.
CREATE TABLE payments (
  id           serial PRIMARY KEY,
  order_id     int  NOT NULL REFERENCES orders(id),
  amount_cents int  NOT NULL,
  method       text NOT NULL,
  status       text NOT NULL
);

INSERT INTO payments (order_id, amount_cents, method, status)
SELECT o.id, p.price_cents * o.quantity,
       (ARRAY['card', 'card', 'wallet'])[1 + (o.id % 3)],
       CASE o.id WHEN 7 THEN 'declined' WHEN 11 THEN 'refunded' ELSE 'captured' END
FROM orders o JOIN products p ON p.id = o.product_id;
