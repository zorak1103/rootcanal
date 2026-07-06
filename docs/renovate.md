# Renovate Bot

How dependency updates are automated in rootcanal.

## What it does

- Tracks `go.mod` (direct + indirect Go module updates, including the `go` toolchain version)
- Tracks GitHub Actions `uses:` pins in `.github/workflows/*.yml`
- Tracks tools installed via `go install pkg@vX.Y.Z` in `.github/workflows/*.yml` and `Taskfile.yml` (golangci-lint, govulncheck) via `customManagers`
- Tracks the pinned `goreleaser-action` version and TruffleHog action version via `customManagers`
- Patch + digest updates: auto-merged after CI goes green
- Minor + major updates: opened as PRs for manual review
- Posts a "Dependency Dashboard" issue as a central status tracker

## Where it runs

- GitHub Actions workflow `.github/workflows/renovate.yml`
- Triggered on a daily schedule (02:00 UTC) and via `workflow_dispatch`
- Config: `renovate.json` at the repo root

## One-time setup (manual, in GitHub UI)

1. **Fine-grained Personal Access Token** — Settings (user) → Developer settings → Personal access tokens → Fine-grained tokens → "Generate new token"
   - Repository access: `zorak1103/rootcanal` only
   - Permissions: **Contents** (read/write), **Pull requests** (read/write), **Issues** (read/write)
   - Copy the generated token string.
   - ⚠️ Use a PAT, not `GITHUB_TOKEN` — PRs opened with `GITHUB_TOKEN` don't trigger CI workflows (GitHub security policy), so automerge and the Dependency Dashboard would silently stop working.

2. **Repository secret** — Repo → Settings → Secrets and variables → Actions → "New repository secret"
   - Name: `RENOVATE_TOKEN`
   - Value: `<token from step 1>`

3. **Allow auto-merge** — Repo → Settings → General → Pull Requests → enable "Allow auto-merge" (required for `platformAutomerge` to take effect).

## Day-to-day operations

| Action | How |
|--------|-----|
| Pause Renovate | Disable the `Renovate` workflow under Actions → Renovate → "..." → Disable workflow |
| Run immediately | Actions → Renovate → "Run workflow" (workflow_dispatch) |
| Dry run | Run locally with `RENOVATE_DRY_RUN=true` env, or temporarily set `dryRun: "full"` in `renovate.json` |
| Change behaviour | Edit `renovate.json`, push via normal PR |

## Token renewal

Fine-grained PATs expire on the date set at creation. When the `Renovate` workflow starts failing with auth errors:
1. Developer settings → Personal access tokens → Fine-grained tokens → regenerate/renew the `renovate-bot` token
2. Update the `RENOVATE_TOKEN` repository secret with the new value
