-- Search: synonyms and boosts the search service loads on reload.
CREATE TABLE search_synonyms (
  term     text PRIMARY KEY,
  synonyms text NOT NULL
);

CREATE TABLE search_boosts (
  product_id int     NOT NULL REFERENCES products(id),
  boost      numeric NOT NULL
);

INSERT INTO search_synonyms (term, synonyms) VALUES
  ('decaf',      'decaffeinated,caffeine free'),
  ('espresso',   'expresso,ristretto'),
  ('grinder',    'mill,burr'),
  ('kettle',     'pot,boiler'),
  ('filter',     'paper,cone'),
  ('subscription', 'plan,monthly');

INSERT INTO search_boosts (product_id, boost) VALUES (3, 2.0), (6, 1.5), (10, 1.8);
