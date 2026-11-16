INSERT INTO plans (code, name, price_cents) VALUES
  ('starter', 'Starter', 1900),
  ('growth',  'Growth',  4900),
  ('scale',   'Scale',   9900);

INSERT INTO subscriptions (account_id, plan_id, seats, started_at, status) VALUES
  (1, 2, 25,  now() - interval '400 days', 'active'),
  (2, 3, 120, now() - interval '370 days', 'active'),
  (3, 2, 18,  now() - interval '290 days', 'active'),
  (4, 3, 300, now() - interval '250 days', 'active'),
  (5, 1, 4,   now() - interval '180 days', 'past_due'),
  (6, 3, 85,  now() - interval '140 days', 'active'),
  (7, 1, 6,   now() - interval '80 days',  'active'),
  (8, 1, 3,   now() - interval '20 days',  'trialing');

-- Two invoices per account: last month paid, this month open or paid.
-- Cobalt Reach is the overdue account for the dunning walkthrough.
INSERT INTO invoices (account_id, amount_cents, issued_at, due_at, status)
SELECT s.account_id, p.price_cents * s.seats, current_date - 45, current_date - 15, 'paid'
FROM subscriptions s JOIN plans p ON p.id = s.plan_id;

INSERT INTO invoices (account_id, amount_cents, issued_at, due_at, status)
SELECT s.account_id, p.price_cents * s.seats, current_date - 15, current_date + 15,
       CASE WHEN s.account_id IN (2, 4) THEN 'paid' ELSE 'open' END
FROM subscriptions s JOIN plans p ON p.id = s.plan_id;

UPDATE invoices SET due_at = current_date - 10, status = 'overdue'
WHERE account_id = 5 AND issued_at = current_date - 15;
