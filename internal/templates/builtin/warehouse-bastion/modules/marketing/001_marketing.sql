-- Marketing mart: five campaigns and the touches they produced.
CREATE TABLE dim_campaign (
  id         int PRIMARY KEY,
  name       text NOT NULL,
  channel    text NOT NULL,
  cost_cents int  NOT NULL
);

CREATE TABLE fact_touches (
  id          serial PRIMARY KEY,
  campaign_id int     NOT NULL REFERENCES dim_campaign(id),
  day         date    NOT NULL REFERENCES dim_date(day),
  is_lead     boolean NOT NULL
);

INSERT INTO dim_campaign (id, name, channel, cost_cents) VALUES
  (1, 'Spring webinar series', 'events', 1200000),
  (2, 'Search: data platform', 'paid',    840000),
  (3, 'Partner co-marketing',  'partner', 300000),
  (4, 'Analyst report',        'content', 450000),
  (5, 'Field dinner series',   'events',  960000);

INSERT INTO fact_touches (campaign_id, day, is_lead)
SELECT 1 + (g % 5), current_date - (g % 90), g % 3 = 0
FROM generate_series(1, 150) AS g;

CREATE VIEW cost_per_lead AS
SELECT c.name, c.channel, count(*) FILTER (WHERE t.is_lead) AS leads,
       c.cost_cents / greatest(count(*) FILTER (WHERE t.is_lead), 1) AS cost_per_lead_cents
FROM dim_campaign c JOIN fact_touches t ON t.campaign_id = c.id
GROUP BY c.id;
