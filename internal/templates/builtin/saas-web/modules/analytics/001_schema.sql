-- Analytics module: product usage events and the weekly active view.
CREATE TABLE events (
  id         serial PRIMARY KEY,
  account_id int NOT NULL REFERENCES accounts(id),
  name       text NOT NULL,
  at         timestamptz NOT NULL
);

CREATE VIEW weekly_active_accounts AS
SELECT a.name, count(e.id) AS events, max(e.at) AS last_event
FROM accounts a JOIN events e ON e.account_id = a.id
WHERE e.at > now() - interval '7 days'
GROUP BY a.name;
