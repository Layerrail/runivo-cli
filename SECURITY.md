# Security

Please use GitHub private vulnerability reporting for this repository. Do not include credentials or customer data in a public issue.

The CLI communicates with Runivo over HTTPS. Local HTTP is allowed only for localhost development. Saved keychain credentials are bound to their API origin and workspace. Redirects never receive bearer credentials. Device codes are PKCE-bound, expire after ten minutes, require explicit browser approval and can be exchanged only once. Issued API keys expire after thirty days and can be revoked in the dashboard or with `runivo logout`.

The OS keychain is the default. On Linux this requires a Secret Service provider such as GNOME Keyring or KWallet. There is no silent plaintext fallback. `--token-file` explicitly opts into a credential file; the CLI creates it with mode 0600 on Unix. On Windows, use a directory restricted to your Windows account. For CI, use a masked secret in `RUNIVO_API_KEY` and a workspace-scoped key with the minimum necessary scope. Never commit a credential file or echo its contents.

Environment variables are masked by default. `env reveal`, `secret-files reveal`, and authenticated raw API calls can disclose secrets deliberately; avoid capturing their output in CI logs. Interactive terminals render the service's terminal output. Run shell sessions only on services you trust.

The CLI contains no cloud, registry, database or Dokploy administrator credentials. Authorization, MFA policies, paid-feature entitlements and quotas are enforced by the server. Billing purchases require dashboard authorization; the CLI does not process payment details.
