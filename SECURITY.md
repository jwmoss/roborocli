# Security Policy

## Reporting

Report security issues privately through GitHub security advisories or by
contacting the maintainer directly.

## Runtime Safety

- Do not print tokens or authorization headers.
- Config files that may contain tokens are written with mode `0600`.
- `--trace-http` logs request metadata only.
- `--dry-run` blocks non-GET requests.
