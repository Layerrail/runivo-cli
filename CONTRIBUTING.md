# Contributing

Use Go 1.27 or later. Run `go test ./...`, `go vet ./...`, and `gofmt -w cmd internal` before opening a pull request. CI also runs the race detector on Windows, macOS and Linux.

Tests use isolated HTTP servers and never require customer credentials. Add API-contract tests for new commands and negative tests for permissions, malformed responses, retries and partial failures. Never mock a queued action as a completed deployment. Changes to the private control plane require a compatible API rollout before a CLI release.

Build locally with `go build ./cmd/runivo`. Maintainers can generate release archives with `python scripts/release.py --version v0.1.0 --commit FULL_COMMIT_ID`. Pushing a version tag runs the test matrix, builds six archives, generates SHA-256 checksums, attests provenance and publishes a GitHub release. Do not tag an unverified backend dependency.
