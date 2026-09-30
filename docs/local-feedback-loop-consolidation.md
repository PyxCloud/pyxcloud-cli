# Structural local feedback loop

## Outcome

A fresh development agent can validate a feature against the real local API, database and identity service before release using documented, versioned commands. No paid product models or real providers are required. Existing shared sandbox ownership and production guards remain authoritative.

## Required protocol

1. Measure repository paths/revisions, fixture integrity, Docker sandbox labels and owner lock. Fail before mutation on missing migration paths, conflicting owner, unexpected tenant, unsafe endpoints or missing runtime capability.
2. Start DB/SSO and apply the explicitly selected database migration chain through the normal migrator. Use an explicit volume and preserve it across backend rebuilds; never silently delete it or adopt a different chain.
3. Build and start the native backend with a clean, explicit local environment. All fixture ports are restricted to offline/dev/local issuer. Keep a private state directory, exact owned PID/binary identity and bounded readiness checks. Logs must not expose secrets.
4. Authenticate via real Keycloak PKCE, bootstrap only verified tenant identity prerequisites, then run public CLI/API acceptance. Keep fixture source evidence separate from user acceptance requirements.
5. Repeat each completed walk and compare authoritative identities. Record compact private evidence, elapsed times, provider mode and failures. Accepted mutations are not completed stages. Preserve uncertain outcomes and reconcile before retries.
6. Stop only the owned process on request. Destructive database reset is a separate explicit operation; the feedback command never deletes shared volumes.

## Atomic delivery order

- Version the native start helper in backend sandbox/specops; eliminate coordinator absolute paths and /tmp-only instructions.
- Version canonical tenant bootstrap tied to verified fixture user and real membership. No artificial project/specification/deployment state writes.
- Add a single CLI-repository feedback entry point with explicit backend/database paths and bounded phase selection. Reuse M0/M1 harnesses, do not duplicate workflow logic. The command must reject unsupported M2-M4 phases until their actual acceptance harnesses ship.
- Add fixture materialization and real scanner tool/worker setup; preserve findings, provenance, fenced execution and tenant isolation.
- Complete M2-M4 runtime ports and acceptance harnesses, then add them to the entry point.
- Provide an agent procedure for quick focused checks followed by affected public API acceptance, with exact invocation and evidence locations. Keep setup/doctor/catalog as the agent connection layer.

## Release gate

Local phase completion requires fresh passing evidence plus resume checks. Real cloud acceptance remains NOT RUN. CI contract drift and existing production checks remain required. A local fixture cannot certify external provider behavior.

## STOP actions

On conflicting owner or tenant, stop before writes. On failed migration/readiness, preserve metadata and stop the phase. On unknown state or uncertain mutation, read authority before retry. On usage exhaustion, checkpoint partial work and leave COMPLETE absent.

| Common mistake | Required action |
|---|---|
| Assume coordinator machine paths | Resolve explicit repository arguments and validate them |
| Rebuild loses sandbox state | Preserve explicit owned state directory and DB volume |
| Agent starts competing stack | Honor shared ownership lock and report contention |
| Report unit tests as E2E | Run the affected real local API phase and retain evidence |
| Missing phase appears successful | Return unsupported until harness and runtime acceptance exist |
