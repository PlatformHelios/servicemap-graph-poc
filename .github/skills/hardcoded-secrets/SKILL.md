---
name: hardcoded-secrets
description: 'Review repository files for hardcoded passwords, API keys, access tokens, private keys, and credentials embedded in URLs. Use for secret scanning, credential leak checks, and pre-commit or pre-push reviews.'
user-invocable: false
---

# Hardcoded Secret Review

Identify plausible real credentials committed in the current repository workspace. This is a read-only review, not a substitute for a dedicated secret-scanning tool or a scan of Git history.

## Procedure

1. Search source code, configuration, scripts, documentation, tests, and relevant generated files for credential assignments, private-key headers, provider token formats, bearer/basic credentials, and URLs containing user information.
2. Inspect each candidate in context. A credential-related identifier or UI string alone is not evidence of a secret.
3. Exclude clear environment-variable references, empty values, templates, and obviously fake examples or test fixtures. If a value could be real but cannot be verified, report it as an uncertain candidate.
4. Do not print or reproduce the complete candidate value. Mask it, and do not attempt to authenticate with it.
5. Report each likely finding with severity, confidence, file and line, credential type or setting name, brief rationale, and remediation. Recommend rotating a credential if it may have been exposed; removing it from the current file alone does not invalidate it.
6. If no likely secrets are found, say so. Note meaningful coverage limitations, including binary or ignored files, generated bundles that could not be inspected, and Git history that was not scanned.

## Triage Guidance

- Treat a plausible live key, token, password, private key, or credential-bearing URL as high severity.
- Treat ambiguous high-entropy values in credential-related settings as candidates requiring verification, not confirmed secrets.
- Do not report conventional placeholders such as `your_password`, `${SECRET}`, or clearly synthetic test values as secrets.
- Avoid flooding the report with matches from vendor bundles or source maps. Inspect likely findings where practical and disclose if generated artifacts materially limit confidence.
