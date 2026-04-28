# Release Checklist

Use this checklist before pushing to a remote, tagging, or preparing a public release.

## Local State

- Confirm the branch is correct:
  ```bash
  git status --short --branch
  ```
- Confirm no generated artifacts are staged:
  ```bash
  git status --short --ignored
  ```
- Confirm ignored local artifacts include `.crucible/`, `.codex`, and `bin/`.

## Validation

- Format code:
  ```bash
  make fmt
  ```
- Run the standard check:
  ```bash
  make check
  ```
- Run the end-to-end smoke test when evaluation or CLI workflow changed:
  ```bash
  make smoke
  ```
- Run the CI-style regression tournament when reports, indexes, or tournament automation changed:
  ```bash
  make regression-tournament
  ```

## Sensitive Data And Artifacts

- Scan the commit candidate surface for credential-looking content:
  ```bash
  rg -n --hidden --glob '!.git/**' --glob '!bin/**' --glob '!**/.crucible/**' --glob '!.codex' --glob '!.cache/**' --glob '!docs/release-checklist.md' -i "(api[_-]?key|secret|token|password|passwd|bearer|authorization|private[_-]?key|BEGIN (RSA|OPENSSH|PRIVATE) KEY|ghp_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+|sk-[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]+|AKIA[0-9A-Z]{16})" .
  ```
- Review any `.crucible/` archives before sharing them. They may contain source, prompts, logs, generated candidates, external traces, or project-specific context.

## Repository Metadata

- Confirm `README.md` reflects current commands and limitations.
- Confirm `LICENSE`, `SECURITY.md`, and `CONTRIBUTING.md` are present.
- Confirm GitHub Actions exists under `.github/workflows/ci.yml` and includes both Go checks and the regression tournament job.
- Confirm the remote target is intentional before the first push:
  ```bash
  git remote -v
  ```
