# Runivo CLI

The official command-line interface for **Runivo**, a product of **LayerRail, Inc.** Manage services using the same control plane, permissions and plan limits as the dashboard.

## Install

Download the archive for your OS and architecture from [Releases](https://github.com/Layerrail/runivo-cli/releases). Verify it against `checksums.txt`, extract it, and put `runivo` (`runivo.exe` on Windows) on your PATH.

With Go 1.27 or later:

```sh
go install github.com/Layerrail/runivo-cli/cmd/runivo@latest
runivo --version
```

On macOS/Linux, verify a downloaded archive with `shasum -a 256 ARCHIVE`. On PowerShell, use `Get-FileHash ARCHIVE -Algorithm SHA256`. Compare the result with the matching release checksum. Releases also include GitHub build-provenance attestations, verifiable with `gh attestation verify ARCHIVE --repo Layerrail/runivo-cli`.

## Sign in and deploy

```sh
runivo login
runivo whoami
runivo services list
runivo link my-web-app
runivo deploy --wait
runivo logs --follow
runivo status --watch
```

Login opens an approval page. Verify the displayed code, choose your workspace and approve access. Keys expire after 30 days and are stored in your OS keychain. Use `login --read-only` for observation or `login --no-browser` on a remote machine. Revoke access with `logout --yes` or in the dashboard's API Keys page.

To authorize another workspace, use `runivo login --profile team-name`, then `runivo workspaces use team-name`. `workspaces list` lists locally authorized profiles, not every workspace on your account. Use one workspace profile per login. Linux keychain access requires Secret Service; headless systems can explicitly use `login --token-file /private/path/token`, and supply the same `--token-file` on later commands.

`link` saves only IDs in a local `runivo.toml`. It does not upload source or change your Git configuration. Deployments build the connected repository or configured container image through Runivo's existing pipeline. Local-directory uploads are not supported by the control plane.

## Create and configure services

```sh
runivo catalog --json
runivo github connections --json
runivo github repositories --connection CONNECTION_ID --json
runivo github detect --repo your-org/your-repo --branch main
runivo services create --file examples/service.json
runivo services update my-web-app --file service-update.json
runivo projects create --name production
runivo environments create --file environment.json
runivo domains create --service my-web-app --name app.example.com
runivo domains verify app.example.com --service my-web-app --yes
```

Creation saves service configuration; add `--deploy` to enqueue its first deployment or run `deploy` separately. Use IDs returned by the API for project/environment assignments. `--file` accepts the API's complete JSON body; `--file -` reads stdin. Plan IDs and service capabilities come from `catalog`. Region availability, quotas and paid features are enforced by Runivo. Workflow configuration or saved schedules do not imply execution support beyond what the backend offers.

## Commands

| Command | Operations |
| --- | --- |
| `login`, `logout`, `whoami`, `workspaces`, `link` | Authentication and local workspace/service context |
| `services`, `projects`, `environments`, `groups` | List, inspect, create, update and delete configuration |
| `deploy`, `deploys` | Deploy, inspect, wait, cancel and roll back |
| `status --watch`, `logs --follow`, `metrics` | Live service events, cursor-based logs and metrics |
| `restart`, `suspend`, `resume`, `open` | Operate a service and open its public URL |
| `env`, `secret-files` | List, set from stdin/file, explicitly reveal and delete secrets |
| `shell`, `jobs` | Interactive terminal, one-off/cron executions, results and cancellation |
| `domains`, `disks`, `headers`, `redirects`, `schedules` | Service resource configuration |
| `backups` | Create, list, download with integrity checks, delete and restore into a new database |
| `integrations`, `registries`, `connections`, `routing`, `scaling` | Networking, integration delivery, registry and runtime information |
| `github` | Connect/install the GitHub app, list repositories/branches and detect frameworks |
| `blueprints` | Create, validate, inspect, update and apply configuration |
| `workspace`, `members`, `invitations`, `audit`, `notifications` | Workspace settings, access and operational history |
| `usage`, `billing` | Consumption, unbilled charges, invoices and dashboard checkout |
| `api` | Any supported workspace API operation using a scoped key |
| `completion` | Bash, zsh, fish and PowerShell completion |

Run `runivo COMMAND --help` for flags and subcommands. Some list endpoints return their latest 100 records. GitHub listing supports `--page`; use `--json` to read `nextPage`. Audit history is limited by workspace retention. Environment-group secrets use `env --group NAME_OR_ID`.

## Secrets and shells

```sh
printf '%s' 'secret-value' | runivo env set API_TOKEN --service my-web-app
runivo env import --file variables.json --service my-web-app --yes
runivo secret-files set credentials.json --file ./credentials.json --service my-web-app
runivo shell --service my-web-app
runivo jobs run --service my-worker --command 'python manage.py check' --wait --yes
```

Secret input preserves bytes, including trailing newlines. Imports apply one key at a time and report partial failure; they are not a transaction across all keys. Shell sessions follow platform entitlements, support terminal resize and Ctrl+C, and disconnect with Ctrl+]. No SSH or Docker credentials are required.

## Automation

```sh
# Store RUNIVO_API_KEY in your CI system's masked secrets, not in this file.
export RUNIVO_WORKSPACE='YOUR_WORKSPACE_UUID'
runivo deploy --service YOUR_SERVICE_UUID --wait --json
```

CI can use an existing workspace API key; `login --token-stdin` imports one into the local credential store. `RUNIVO_API_KEY` overrides the keychain. An explicit `--token-file` overrides that environment key. Context flags: `--workspace/-w`, `--service/-s`, `--profile`, `--api-url`, `--token-file`, `--json`, `--yes/-y`. Environment equivalents: `RUNIVO_WORKSPACE`, `RUNIVO_SERVICE`, `RUNIVO_PROFILE`, `RUNIVO_API_URL`, `RUNIVO_TOKEN_FILE`, `RUNIVO_CONFIG_DIR`.

Context precedence is explicit flags/environment, then local `runivo.toml` for workspace/service, then the active profile. A saved credential is never sent to a different API origin. Default API origin: `https://runivo-dashboard.vercel.app`.

JSON output goes to stdout; errors and progress go to stderr. Follow/watch commands emit newline-delimited JSON. Use `--yes` for destructive operations in scripts. A deployment wait timeout stops the CLI wait, not the deployment. Resume with `deploys wait DEPLOYMENT_ID`. The CLI does not automatically retry mutations after an uncertain network response.

| Exit | Meaning |
| --- | --- |
| 0 | Command succeeded; queued actions remain asynchronous unless `--wait` was used |
| 1 | Invalid request, network or other error |
| 2 | Confirmation declined |
| 3 | Authentication required or expired |
| 4 | Permission or entitlement denied |
| 5 | Resource not found |
| 6 | Conflict |
| 7 | Rate limited |
| 8 | Deployment wait timed out |
| 9 | Deployment/job failed, cancelled or superseded |
| 130 | Interrupted |

## Advanced API access

```sh
runivo api services/SERVICE_ID/scaling --json
runivo api services/SERVICE_ID/references --json
runivo api services/SERVICE_ID/routing -X POST --yes
runivo api github/bind -X POST --file installation.json --yes
```

Paths are relative to the authorized workspace. This does not bypass permissions or make unavailable features executable. Payment methods, purchases and plan changes must be authorized in the dashboard with `runivo billing open`. The CLI reads existing billing records and does not charge cards or issue promotional credits.

MIT licensed. See [SECURITY.md](SECURITY.md) and [CONTRIBUTING.md](CONTRIBUTING.md).
