-- Ten tickets; only the urgent SSO ticket is past its SLA.
INSERT INTO tickets (account_id, subject, priority, status, opened_at) VALUES
  (1, 'Export to CSV times out on large reports', 'high',   'open',    now() - interval '6 hours'),
  (2, 'SSO login loops for new users',            'urgent', 'open',    now() - interval '7 hours'),
  (2, 'Audit log retention to two years',         'normal', 'pending', now() - interval '30 hours'),
  (3, 'Invoice shows the wrong tax region',       'normal', 'solved',  now() - interval '4 days'),
  (4, 'Dashboard slow for the APAC stores',       'high',   'open',    now() - interval '10 hours'),
  (4, 'Add 40 seats before the holiday rush',     'normal', 'solved',  now() - interval '2 days'),
  (5, 'Card declined on renewal',                 'high',   'pending', now() - interval '3 hours'),
  (6, 'API rate limit for the trading desk',      'normal', 'open',    now() - interval '20 hours'),
  (7, 'Change the account owner',                 'normal', 'solved',  now() - interval '6 days'),
  (8, 'How do I invite the repair team',          'normal', 'open',    now() - interval '1 hour');
