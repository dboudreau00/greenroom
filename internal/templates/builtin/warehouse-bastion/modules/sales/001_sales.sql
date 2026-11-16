-- Sales mart: twelve customers across four regions, sixty orders.
CREATE TABLE dim_customer (
  id     int PRIMARY KEY,
  name   text NOT NULL,
  region text NOT NULL
);

CREATE TABLE fact_orders (
  id           serial PRIMARY KEY,
  customer_id  int  NOT NULL REFERENCES dim_customer(id),
  day          date NOT NULL REFERENCES dim_date(day),
  amount_cents int  NOT NULL
);

INSERT INTO dim_customer (id, name, region)
SELECT g, 'Customer ' || g, (ARRAY['NA', 'EMEA', 'APAC', 'LATAM'])[1 + (g % 4)]
FROM generate_series(1, 12) AS g;

INSERT INTO fact_orders (customer_id, day, amount_cents)
SELECT 1 + (g % 12), current_date - (g * 2 % 119), 5000 + (g * 1733 % 90000)
FROM generate_series(1, 60) AS g;

CREATE VIEW revenue_by_region AS
SELECT c.region, count(o.id) AS orders, sum(o.amount_cents) AS revenue_cents
FROM fact_orders o JOIN dim_customer c ON c.id = o.customer_id
GROUP BY c.region;
