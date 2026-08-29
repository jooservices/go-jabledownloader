# Security

## Reporting

Report vulnerabilities privately to the JOOservices maintainers instead of
opening a public issue.

## Practices

- Credentials (OBS ingestion user, root passwords) never live in the
  repository; `.env` is gitignored, `.env.example` documents shape only.
- Telemetry is off by default and fail-open; a misconfigured or unreachable
  OBS endpoint can only degrade observability, never availability.
- Downloads are validated against expected HTTP status codes and retried
  with backoff; partial files are written via temp file + rename.
- Self-update verifies asset size after download and keeps a `.old` backup
  until the new binary is in place.
- No test or code path embeds secrets, tokens, or hard-coded credentials.
