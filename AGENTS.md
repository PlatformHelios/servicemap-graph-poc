# Repository Guidance

- Never hardcode secrets, credentials, API keys, access tokens, or private keys in source, configuration, documentation, tests, or examples.
- Never write real secrets to files, logs, command output, or chat. Use an approved secret manager or environment-based configuration; use clearly fake placeholders in examples and tests.
- If a secret is found in the repository, do not repeat it. Report its location without the value and recommend revoking or rotating it.
- Make the smallest focused change that solves the request. Follow existing code and project conventions; avoid unrelated cleanup.
- Before editing, check relevant existing instructions and nearby code. Preserve user changes and do not overwrite unrelated work.
- Validate changes with the most relevant available tests or checks. Report what you ran and any checks you could not run.
- Do not commit, push, or perform destructive operations unless explicitly asked.
- For reviews, lead with actionable findings and include file and line references. Do not claim a check passed if it was not performed.
