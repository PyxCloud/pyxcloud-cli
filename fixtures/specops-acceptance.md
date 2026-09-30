# Local fixture acceptance: HTTP health endpoint

Source classification: **user acceptance fixture**. This document states the M1 harness input; it is not repository proof and does not claim that a discovery result verified the implementation.

## Functional requirements

- **FR-01 — Health endpoint:** `GET /healthz` returns HTTP 200 with body `ok` when the optional database endpoint is unset or reachable. When the endpoint is configured but unreachable, the handler returns HTTP 503.

## Invariants

- **INV-01 — Bounded health check:** The optional database reachability check uses a one-second connection timeout and does not turn an unavailable database into a successful health response.

## Acceptance checks

- **AC-01:** With no optional database endpoint configured, request `GET /healthz`; expect status 200 and body `ok`.
- **AC-02:** With a reachable optional database endpoint configured, request `GET /healthz`; expect status 200 and body `ok`.
- **AC-03:** With an unreachable optional database endpoint configured, request `GET /healthz`; expect status 503.

The checks describe the explicitly approved local fixture behavior. They do not add product identity, users, business purpose, availability promises, or other requirements.
