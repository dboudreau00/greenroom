-- cfo@v2: the view the finance dashboard reads during the quarter-end demo.
CREATE VIEW quarter_close AS
SELECT
  date_trunc('quarter', o.placed_at)::date        AS quarter,
  c.segment,
  count(*)                                        AS orders,
  sum(o.amount_cents) FILTER (WHERE o.status = 'paid')   AS paid_cents,
  sum(o.amount_cents) FILTER (WHERE o.status = 'open')   AS open_cents,
  sum(o.amount_cents) FILTER (WHERE o.status = 'refund') AS refunded_cents
FROM orders o
JOIN customers c ON c.id = o.customer_id
GROUP BY 1, 2
ORDER BY 1, 2;
