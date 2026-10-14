---
name: sdlc-review
description: 'Review a repository or proposed change against PlatformHelios SDLC guidance before commit or push. Use for SDLC compliance, secure code review, testing, supply-chain, AI governance, and release-readiness reviews.'
user-invocable: false
---

# PlatformHelios SDLC Review

Review the requested repository scope against the current guidance in [PlatformHelios/sdlc-docs](https://github.com/PlatformHelios/sdlc-docs). This is a read-only review; do not modify files, run project code, approve changes, or claim operational controls were completed without evidence.

## Source of Truth

1. Use the web tool to retrieve the current versions from the `main` branch of the SDLC overview and relevant pages under `content/docs/sdlc/`. Start with `_index.md`; select phase documents based on the changed code and repository evidence. Commonly relevant pages include `requirements-risk.md`, `architecture-review.md`, `ai-assisted-development.md`, `code-review.md`, `automated-testing.md`, `supply-chain-security.md`, `security-validation.md`, `change-management.md`, `deployment.md`, and `monitoring-audit.md`. Consult `penetration-testing.md`, `red-team.md`, or `user-acceptance-testing.md` when the change or requested scope warrants them, and `ai-governance.md` when AI was used.
2. Fetch only public SDLC documentation. Never send repository contents, candidate secrets, or other private data to external services.
3. If the source cannot be reached or a page is unclear, state that limitation and do not invent or silently substitute requirements. Include links to the SDLC pages used and identify the branch as `main`.

## Review Procedure

1. Determine the review scope from the request. If unspecified, focus on current workspace changes and inspect nearby code/configuration only as needed to judge impacts.
2. Map relevant changes to explicit SDLC controls. Prioritize code-review requirements (functional correctness, error handling, authentication, authorization, secrets management, input validation, compliance, audit, and logging), automated testing, dependency/container supply chain, and security validation.
3. Inspect repository evidence such as tests, CI workflows, dependency manifests, container/build configuration, and documentation. Do not execute tests, scanners, builds, deployments, or other project commands; this agent is read-only and lacks execution tools. State when execution evidence is unavailable.
4. Treat the 80–90% unit-test coverage figure in the SDLC testing page as a target. Only assess it when repository coverage evidence is available; do not infer coverage from the presence of tests.
5. Separate source-verifiable findings from process or release gates. Human peer review, requirements/risk approval, architecture-board approval, AI prompt/code attribution, CAB records, UAT, production separation of duties, penetration/red-team results, deployment validation, monitoring, and audit retention may not be inferable from repository files. Mark these as **Not assessed** or **Evidence not found**, not as passed or failed, unless direct evidence is present.
6. Do not claim a control is satisfied merely because a configuration or workflow exists. Report what the inspected evidence demonstrates and what remains unverified.

## Output

Lead with actionable findings ordered by severity. For each, include severity, confidence, repository file and line when applicable, the relevant SDLC control with a link to its source page, rationale, and a concise remediation.

After findings, list significant SDLC gates that are not assessable from the repository scope and checks that could not be executed. If no findings are identified, say so while preserving those limitations. Do not repeat secret values or sensitive contents in the report.
