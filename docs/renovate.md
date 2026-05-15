# Renovate Bot

How dependency updates are automated in rootcanal.

## What it does

- Tracks `go.mod` (direct + indirect Go module updates)
- Tracks Docker images in `.gitlab-ci.yml` (`golang:`, `goreleaser/goreleaser:`, `renovate/renovate:`)
- Tracks the `GO_VERSION` variable in `.gitlab-ci.yml`
- Tracks tools installed via `go install pkg@vX.Y.Z` in `.gitlab-ci.yml`
- Patch + digest updates: auto-merged after the CI pipeline goes green
- Minor + major updates: opened as MRs for manual review
- Posts a "Dependency Dashboard" issue as a central status tracker

## Where it runs

- GitLab CI job `renovate` in stage `maintenance`
- Triggered exclusively by the nightly Pipeline Schedule (03:00 UTC)
- Config: `renovate.json5` at the repo root

## One-time setup (manual, in GitLab UI)

1. **Project Access Token** — Settings → Access Tokens → "Add new token"
   - Name: `renovate-bot`
   - Role: `Developer`
   - Scopes: `api`, `read_repository`, `write_repository`
   - Copy the generated token string.

2. **CI/CD variable** — Settings → CI/CD → Variables → "Add variable"
   - Key: `RENOVATE_TOKEN`
   - Value: `<token from step 1>`
   - Type: Variable
   - Flags: **Masked** ✔, **Protected** ✔, Expand variable reference ✘

3. **Pipeline Schedule** — Build → Pipeline schedules → "New schedule"
   - Description: `Renovate (nightly)`
   - Interval Pattern (Custom): `0 3 * * *` (03:00 UTC = 04:00/05:00 CET/CEST)
   - Cron Timezone: `[UTC] UTC`
   - Target Branch: `main`
   - Activated ✔

## Day-to-day operations

| Action | How |
|--------|-----|
| Pause Renovate | Deactivate the Pipeline Schedule in Build → Pipeline schedules |
| Run immediately | "Play" on the schedule entry in Pipeline schedules |
| Dry run (no MRs) | Set variable `RENOVATE_DRY_RUN=true` in the Schedule's Variables section |
| Change behaviour | Edit `renovate.json5`, push via normal MR |

## Token renewal

Project Access Tokens expire. When the `renovate` job starts failing with auth errors:
1. Settings → Access Tokens → revoke old `renovate-bot` token
2. Create a new one with the same scopes
3. Update the `RENOVATE_TOKEN` CI/CD variable
