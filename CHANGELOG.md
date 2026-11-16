# Changelog

## 0.3.0

Environment templates.

- `greenroom templates` and `greenroom new <env> --template <name> --modules a,b`
  create an environment from a pre-rolled template for a product type, with
  only the product modules the demo will show. Each module adds its
  dependencies, drift checks, seed steps and verify checks; each persona
  becomes a seed set.
- Built-in templates: `saas-web` (Docker Compose, five modules),
  `warehouse-bastion` (EC2 bastion and SSM, three data marts) and
  `k8s-microservices` (Kubernetes or EKS, four services). Workspace templates
  under `templates/` add to or replace them.
- Portal: New environment dialog with template cards, module checkboxes,
  persona and settings; template and modules shown on cards and headers.
- API: `GET /api/v1/templates`, `POST /api/v1/environments`.
- Drift checks take `personas` to apply only to the seed sets that create
  their data.

## 0.2.0

Portal redesign: stage-light visual system, dark and light themes,
CSS-driven motion.

## 0.1.1

Review fixes: shared environment lock across reloads, copy steps create the
target directory, zero-replica workloads count as down, terminating pods
skipped for exec, failed resets and vanished seed data reported as stale.

## 0.1.0

First release: versioned seed sets, resets, pre-call checks, stale report,
portal, Docker, Kubernetes, EC2, SSH and SQL targets.
