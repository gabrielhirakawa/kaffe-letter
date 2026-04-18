# Contributing

`kaffe-letter` is meant to be easy to run locally and easy to self-host. Contributions should preserve that goal.

## Local Development
- Use Go 1.22 or newer.
- Run the app with `go run ./cmd/newsletter`.
- Run the admin with `go run ./cmd/newsletter --mode server`.
- Build before opening a PR with `go build ./...`.
- Use `.env` or environment variables for first boot and headless setups.
- Do not remove the SQLite/admin path for runtime configuration.

## Code Style
- Format Go code with `gofmt`.
- Keep changes small and focused.
- Prefer clear names over clever abstractions.
- Do not commit generated files, secrets or local databases.
- Preserve the precedence rule: env bootstraps, SQLite/admin overrides after setup.

## UI Changes
- Preserve the existing visual language unless you are intentionally changing branding.
- Keep the admin self-hosted and usable on small screens.
- Prefer simple, explicit interactions over hidden behavior.
- If a setting is editable in the admin, document whether env still acts as bootstrap only.

## Pull Requests
- Explain the user impact and the operational impact.
- Include screenshots for UI changes when relevant.
- Mention any config or migration steps needed to test the change.
