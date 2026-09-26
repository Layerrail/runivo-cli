# Unreleased

MySQL creation supports `--plan mysql-free` and defaults to Free when no plan is supplied. Backup restoration accepts `--plan` so customers can explicitly select a paid recovery instance when their workspace's free MySQL slot is occupied. Selecting a paid target still requires checkout in the dashboard.

# v0.1.2

Keep CLI writes compatible with the current Runivo API's idempotency contract. Send receipt headers only to supported core endpoints and reuse a command's body request ID in its header. This fixes rejected restart, suspend, resume and rollback requests, along with writes to endpoints that do not accept receipt headers.

Live acceptance covered a repository build through HTTP health verification, deployment logs, suspension and a checksummed PostgreSQL backup download. Interactive shell execution and restoration into a new database still require paid-instance acceptance testing; see [verification details](docs/verification.md).

# v0.1.1

Correct version reporting for installations made with `go install`. Release archives continue to embed their exact version and commit.

# v0.1.0

Initial Runivo CLI release for Windows, macOS and Linux (amd64 and arm64).

- Browser authorization with PKCE, revocable workspace API keys, OS credential storage, and CI token support.
- Services, projects, environments, configuration, deployment history, cancellation, rollback, jobs, live status and logs.
- Encrypted environment variables and files, interactive terminals, domains, backups, integrations, GitHub discovery, and blueprints.
- Workspace usage, billing inspection, invoice downloads, and browser-authorized checkout.
- JSON/NDJSON output, shell completion, confirmations, bounded HTTP responses and stable exit codes.

Paid features continue to require the appropriate Runivo plan and workspace role. This CLI uses the existing Runivo control-plane API and does not provide direct engine or infrastructure access.
