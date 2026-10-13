# Secret scanning

This repo is public and runs on a free plan, so secret scanning here uses no paid product.

## What GitHub gives for free, and what it does not

Per GitHub's docs ([about secret scanning](https://docs.github.com/en/code-security/secret-scanning/introduction/about-secret-scanning), [about push protection](https://docs.github.com/en/code-security/secret-scanning/introduction/about-push-protection), checked 13 Oct 2026):

- **Free on public repos:** secret scanning alerts, which only recognise token formats that service providers registered with GitHub. A credential in any other format (ChainRails keys, our own JWT/encryption keys, a generic `"name": ..., "value": ...` pair in a task definition) raises no alert.
- **Free, but narrow:** push protection *for users* is on by default and covers only the pushing user's own pushes to public repos. It does not cover a bot, a collaborator who turned it off, or a commit made through the API.
- **Paid (GitHub Secret Protection):** push protection *for repositories*. We do not use it and nothing here depends on it.

## What this repo uses instead

[gitleaks](https://github.com/gitleaks/gitleaks), pinned to v8.30.1, runs offline with no account. The pin is the release tag, plus the SHA-256 of the release tarball in `.github/workflows/gitleaks.yml` and the tag's commit SHA in `.pre-commit-config.yaml`.

- **CI** (`.github/workflows/gitleaks.yml`): scans the commits a push or pull request adds, where any finding fails the job. It then scans the history reachable from the checked-out commit and fails only on a finding that is not in `.gitleaks-baseline.json`. It prints rule, file:line, commit and date, never the value.
- **Locally** (`.pre-commit-config.yaml`): `pre-commit install` once, and the same gitleaks version and `.gitleaks.toml` scan staged changes before each commit. `git commit --no-verify` skips it, which is why CI is the check that blocks.
- **Config** (`.gitleaks.toml`): the stock rules plus one rule for secret-named `name`/`value` pairs, the shape ECS task definitions use. There are no path or regex allowlists on purpose.

## The baseline is not a clean bill of health

`.gitleaks-baseline.json` lists every finding that existed in history when the scanner was added, so CI starts green. Most are test fixtures, placeholders and public chain contract addresses, but it also includes credential-shaped values, and entries are only safe to leave in the baseline once the credential is rotated. Rotating is the fix; the history stays public regardless.

To baseline a new finding, rotate the credential, then merge the new entries into the baseline in a pull request a reviewer reads:

```sh
gitleaks detect --config .gitleaks.toml --redact=100 --log-opts="--full-history HEAD" \
  --baseline-path .gitleaks-baseline.json --report-path new.json
```

gitleaks reads only the diff of non-merge commits, so a secret that exists only in a merge-conflict resolution is not seen, and it cannot tell whether a credential is live. CI does not scan branches that are not an ancestor of the commit under test.
