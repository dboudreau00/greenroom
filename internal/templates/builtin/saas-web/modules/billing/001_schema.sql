-- Billing module: plans, one subscription per account, monthly invoices.
CREATE TABLE plans (
  id          serial PRIMARY KEY,
  code        text NOT NULL UNIQUE,
  name        text NOT NULL,
  price_cents int  NOT NULL
);

CREATE TABLE subscriptions (
  id         serial PRIMARY KEY,
  account_id int NOT NULL REFERENCES accounts(id),
  plan_id    int NOT NULL REFERENCES plans(id),
  seats      int NOT NULL,
  started_at timestamptz NOT NULL,
  status     text NOT NULL
);

CREATE TABLE invoices (
  id           serial PRIMARY KEY,
  account_id   int NOT NULL REFERENCES accounts(id),
  amount_cents int NOT NULL,
  issued_at    date NOT NULL,
  due_at       date NOT NULL,
  status       text NOT NULL
);

CREATE VIEW mrr_by_plan AS
SELECT p.name AS plan, count(*) AS subscriptions, sum(p.price_cents * s.seats) AS mrr_cents
FROM subscriptions s JOIN plans p ON p.id = s.plan_id
WHERE s.status = 'active'
GROUP BY p.name;
