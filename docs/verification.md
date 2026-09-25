# Initial release verification

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

No billable deployments or payments were initiated during these smoke tests. Interactive shell execution, live rollback, new builds, database export/restore and provider delivery depend on the corresponding running Runivo services and entitlements; this release verification does not claim a new live infrastructure test of those operations. The CLI implements their existing API contracts and reports server-side availability and errors.
