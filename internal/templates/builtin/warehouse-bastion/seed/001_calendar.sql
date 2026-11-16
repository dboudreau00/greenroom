-- Calendar dimension shared by every mart: the last 120 days.
CREATE TABLE dim_date (
  day     date PRIMARY KEY,
  month   text NOT NULL,
  weekday int  NOT NULL
);

INSERT INTO dim_date (day, month, weekday)
SELECT d::date, to_char(d, 'YYYY-MM'), extract(isodow FROM d)::int
FROM generate_series(current_date - 119, current_date, interval '1 day') AS d;
