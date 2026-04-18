# Security

## Secrets
- Do not commit `.env`, `data/master.key`, SQLite databases or any secret material.
- Sensitive values are expected to be stored encrypted by the app, but the repo should still stay clean of production secrets.
- Environment variables may be used for bootstrap on headless servers, but they should not be treated as a secret store for long-term runtime configuration.
- After the first boot, the SQLite/admin layer becomes the source of truth for operational settings.

## Reporting Issues
- For vulnerabilities or secret-handling concerns, open a private security issue or contact the maintainer directly.
- Include reproduction steps, affected version and the expected impact.

## Operational Guidance
- Rotate API keys and SMTP credentials if they were exposed.
- Treat `data/master.key` as part of the backup set, not as a shareable artifact.
- If you deploy without a UI, keep the bootstrap `.env` or environment secrets under the same operational controls as any other secret material.
