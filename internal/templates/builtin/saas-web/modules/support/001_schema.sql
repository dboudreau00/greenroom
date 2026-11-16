-- Support module: tickets and a backlog view that flags SLA breaches.
CREATE TABLE tickets (
  id         serial PRIMARY KEY,
  account_id int NOT NULL REFERENCES accounts(id),
  subject    text NOT NULL,
  priority   text NOT NULL,
  status     text NOT NULL,
  opened_at  timestamptz NOT NULL
);

CREATE VIEW ticket_backlog AS
SELECT t.id, a.name AS account, t.subject, t.priority, t.status, t.opened_at,
       t.status <> 'solved' AND t.opened_at < now() - CASE t.priority
         WHEN 'urgent' THEN interval '4 hours'
         WHEN 'high'   THEN interval '24 hours'
         ELSE interval '72 hours'
       END AS sla_breached
FROM tickets t JOIN accounts a ON a.id = t.account_id;
