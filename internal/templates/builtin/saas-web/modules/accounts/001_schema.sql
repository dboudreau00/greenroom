-- Accounts module: customer accounts and the people who sign in to them.
CREATE TABLE accounts (
  id         serial PRIMARY KEY,
  name       text NOT NULL,
  segment    text NOT NULL,
  region     text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id           serial PRIMARY KEY,
  account_id   int NOT NULL REFERENCES accounts(id),
  email        text NOT NULL UNIQUE,
  role         text NOT NULL,
  last_seen_at timestamptz
);
