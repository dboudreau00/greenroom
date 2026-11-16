-- Finance mart: twelve balanced journal entries, two lines each.
CREATE TABLE dim_account (
  code text PRIMARY KEY,
  name text NOT NULL
);

CREATE TABLE fact_ledger (
  id           serial PRIMARY KEY,
  entry        int  NOT NULL,
  account_code text NOT NULL REFERENCES dim_account(code),
  day          date NOT NULL REFERENCES dim_date(day),
  amount_cents int  NOT NULL
);

INSERT INTO dim_account (code, name) VALUES
  ('1000', 'Cash'),
  ('1200', 'Accounts receivable'),
  ('4000', 'Subscription revenue'),
  ('6100', 'Cloud hosting');

-- Debits are positive, credits negative; every entry nets to zero.
INSERT INTO fact_ledger (entry, account_code, day, amount_cents)
SELECT g, CASE WHEN g % 2 = 0 THEN '1200' ELSE '6100' END, current_date - (g * 7 % 119), 250000 + g * 1000
FROM generate_series(1, 12) AS g;

INSERT INTO fact_ledger (entry, account_code, day, amount_cents)
SELECT g, CASE WHEN g % 2 = 0 THEN '4000' ELSE '1000' END, current_date - (g * 7 % 119), -(250000 + g * 1000)
FROM generate_series(1, 12) AS g;
