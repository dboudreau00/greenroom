-- Inventory module: products, warehouses, stock and the reorder list.
CREATE TABLE products (
  id            serial PRIMARY KEY,
  sku           text NOT NULL UNIQUE,
  name          text NOT NULL,
  reorder_point int  NOT NULL
);

CREATE TABLE warehouses (
  id   serial PRIMARY KEY,
  code text NOT NULL UNIQUE,
  city text NOT NULL
);

CREATE TABLE stock (
  product_id   int NOT NULL REFERENCES products(id),
  warehouse_id int NOT NULL REFERENCES warehouses(id),
  units        int NOT NULL,
  PRIMARY KEY (product_id, warehouse_id)
);

CREATE VIEW reorder_list AS
SELECT p.sku, p.name, sum(s.units) AS units, p.reorder_point
FROM products p JOIN stock s ON s.product_id = p.id
GROUP BY p.id
HAVING sum(s.units) < p.reorder_point;
