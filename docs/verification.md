# CLI verification

## Workload acceptance for v0.1.2

Verified on September 25, 2026 using the live Runivo API, an existing operations test service and the existing warm builder.

- `deploy --wait` fetched the configured public repository, built and uploaded its image, deployed it, observed its health checks and exited successfully at `live`.
- The application returned HTTP 200 with its expected HTML over HTTPS.
- Deployment logs included the clone/build output and the final live-service URL.
- `backups download` exported an existing PostgreSQL backup from private storage. Its SHA-256 matched the API metadata and its file header was the expected PostgreSQL custom dump signature (`PGDMP`). This verifies export integrity, not a restore round trip.
- `suspend --yes` queued an actual runtime operation; the service returned to its original suspended state.
- Interactive `shell` reached the live API and was rejected because the instance lacked an active paid entitlement. No shell commands ran on a workload.
- `backups restore --name ... --yes` reached the live API and was rejected because the new paid instance had not been accepted in Billing. No restore target service remained after the rejected request.
- The temporary, one-hour workspace key was revoked with `logout` and its local token file was removed.

For this small Express application, queue-to-live time was 62.9 seconds (57.0 seconds after the worker started). Logs reported 0.99 seconds for the Git fetch and 15.08 seconds for image build/upload; the recorded health-check phase took 14.35 seconds. These are one application's measurements with an existing registry cache and warm builder, not a general build-time guarantee. The other time includes queueing, preparation and orchestration.

This run found and fixed a CLI/API compatibility issue: core receipt headers must match body request IDs, and unsupported endpoints must not receive those headers. Regression tests cover both cases.

### Remaining workload checks

The validation workspace has no confirmed live payment method or accepted paid compute instance. Before claiming full operational validation, use authorized paid test instances to exercise:

- A real interactive terminal: input/output, resizing, interruption, disconnect and session cleanup.
- A backup restored into an isolated new database, with known source records checked after restoration and source data verified unchanged.
- Live rollback and other provider-dependent operations not exercised by this run.

The tests above confirmed the paid-feature boundaries. They did not bypass them or exercise successful shell execution/database restoration.

## Initial release verification

Verified on September 25, 2026 against the live Runivo control-plane API.

- Browser device authorization succeeded with both read and write scopes.
- Windows OS keychain storage and an explicitly selected token file both worked.
- `whoami`, catalog, service/project listings, usage and billing returned real API data.
- Workspace SSE delivered a live services event.
- A read-only key could not create a service or access another workspace.
- An isolated free workspace was used to create, inspect, rename, archive, restore and delete a draft service and project.
- Encrypted environment values were set, revealed with explicit approval, updated and deleted through the CLI.
- Draft-service logs, deployment history and metrics returned correctly.
- CLI logout revoked both temporary keys. A subsequent request using the revoked token returned HTTP 401.

Automated tests cover API-origin validation, bearer redirect protection, bounded responses, OAuth polling states, command payloads, deployment success/failure, log cursors, secret updates, confirmations, exit codes, SSE parsing, configuration permissions and download integrity. GitHub Actions tests and race detection passed on Linux, macOS and Windows. All six OS/architecture binaries cross-compiled successfully.

No deployments or payments were initiated during the initial smoke tests. The later workload acceptance above adds a real free-service build and database export; the remaining checks are explicitly listed above. The CLI implements the existing API contracts and reports server-side availability and errors.
