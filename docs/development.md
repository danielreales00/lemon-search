# Development guide

Conventions, the quality stack, and the install/run loop.

## One-time setup

```bash
# Tooling
brew install lefthook gitleaks gofumpt golangci-lint
go install github.com/fe3dback/go-arch-lint@latest
go install mvdan.cc/gofumpt@latest

# Repo
git clone https://github.com/danielreales00/lemon-search.git
cd lemon-search
cp .env.example .env.local
npm install                        # root: commitlint + markdownlint
cd web && npm install && cd ..    # next/eslint/knip/etc.
cd api && go mod tidy && cd ..    # Go deps

# Hooks
lefthook install
```

You can verify everything is wired by running:

```bash
lefthook run pre-commit --all-files
lefthook run pre-push  --all-files
```

## Day-to-day loop

| Action | What runs |
|---|---|
| `git commit -m "feat(api): …"` | commitlint (commit-msg); then on pre-commit: gofumpt diff, golangci-lint fast, go-arch-lint, eslint+prettier, madge cycles, markdownlint, gitleaks staged, big-file/env guards |
| `git push` | golangci-lint full, go-arch-lint full, go test -race, go build, tsc, eslint full, knip (dead code/exports/deps), gitleaks history |
| Open PR | CI runs all the pre-push checks plus migrations-idempotency, web build, conventional-commit history check |

## Quality gates (what stops a bad change)

### Correctness
- **Go**: `errcheck`, `staticcheck` (whole-program), `govet` (enable-all), `errorlint`, `bodyclose`, `sqlclosecheck`, `rowserrcheck`, `contextcheck`, `nilerr`, `noctx`, `exhaustive`.
- **TS**: `@typescript-eslint/strict-type-checked` + `stylistic-type-checked`, `no-floating-promises`, `no-misused-promises`, strict `tsconfig` (`noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`).
- **SQL**: migrations applied **twice** in CI; idempotency required.

### Complexity
- **Go**: `gocyclo` (≤ 12), `gocognit` (≤ 15), `funlen` (80 lines / 50 stmts), `nestif` (≤ 5), `cyclop` package average ≤ 8.
- **TS**: ESLint `complexity` (≤ 12), `max-depth` (≤ 4), `max-lines-per-function` (warn at 80), `sonarjs/cognitive-complexity` (≤ 15).

### Dead code
- **Go**: `unused` (golangci-lint) + `unparam`.
- **TS**: `knip` (unused files, exports, deps, devDeps, binaries, types) on pre-push and CI.
- **CSS/JSON/YAML**: not lint-gated; rely on review.

### Architectural drift
- **Go primary**: `api/.go-arch-lint.yml` declares 9 components (`domain`, `api`, `intent`, `rank`, `config`, `observ`, `retrieve-postgres`, `cmd-api`, `cmd-ingest`) and their allowed deps. `domain` is pure (no vendor deps either). `retrieve-postgres` is the only adapter allowed to touch `pgx`. `cmd-*` are the only composition roots.
- **Go baseline**: `depguard` rules in `.golangci.yml` re-state the same boundaries (cheap layer that always runs).
- **TS primary**: `eslint-plugin-boundaries` defines `app` / `component` / `lib` element types with explicit allow rules.
- **Both**: cycle detection — Go via `go vet`/`staticcheck`; TS via `import/no-cycle` + `madge --circular`.

### Duplication
- **Go**: `dupl` (threshold 120 tokens), `goconst` (≥ 4 occurrences).
- **TS**: `sonarjs/no-duplicate-string` (threshold 4), `sonarjs/no-identical-functions`.

### Style & magic
- **Go**: `gofumpt` (stricter than `gofmt`), `revive`, `stylecheck`, `tagliatelle` (snake_case JSON/YAML), `importas` (forced aliases for `pgx`, `pgxpool`), `mnd` (magic numbers blocked).
- **TS**: `prettier` 100-col, `unicorn` modern-JS rules, `unicorn/filename-case` (kebab + Pascal), `import/order` alphabetized with newlines between groups.

### Convention
- **Commits**: `commitlint` enforces `type(scope): subject`. Allowed types: `feat fix perf refactor docs test build ci chore style revert rank bench data`. Allowed scopes: `api web ingest schema config rank intent retrieve observ bench ci hooks docs deps repo`.
- **Markdown**: `markdownlint-cli2` (per `.markdownlint.json`).

### Secrets
- **`gitleaks`** with `.gitleaks.toml` (default ruleset + project-specific Supabase JWT / Fly token rules). Staged diff on commit, history on push, full history in CI.

## Architectural boundaries reference

```
api/internal/
  domain/          ← PURE (no vendor deps either)
  observ/          ← leaf utility (no internals)
  config/          ← may depend on: domain (+ yaml vendor)
  intent/          ← may depend on: domain
  rank/            ← may depend on: domain, config
  retrieve/postgres/ ← may depend on: domain (+ pgx, pgxpool vendors)
  api/             ← may depend on: domain, intent, rank, config, observ, retrieve-postgres
api/cmd/
  api/             ← composition root: may depend on everything
  ingest/          ← composition root for CLI: domain, retrieve-postgres, config, observ
```

```
web/
  app/         ← may depend on: app, component, lib
  components/  ← may depend on: component, lib
  lib/         ← may depend on: lib (i.e. leaf)
```

Violating any of these causes a hook failure or a CI failure.

## Commit message format

```
type(scope): short imperative subject

Optional body explaining the why (not the what — the diff has the what).

Optional footer (Refs, Closes, Co-authored-by, BREAKING CHANGE).
```

Examples:

- `feat(rank): hard-pin exact-name path (score=+Inf)`
- `fix(ingest): handle escaped quotes in stream parser`
- `perf(retrieve): bbox pre-filter before earth_distance`
- `data(schema): add friend_count denormalized column`
- `rank(config): bump claimed weight for high-stakes archetype`
- `bench(intent): add "wedding photographer" expected-top-3`

Anything else (no scope, capital subject, trailing period, wrong type) is blocked at commit-msg.

## Local commands you'll use

```bash
# Go
cd api && make fmt lint test build
cd api && go-arch-lint check --project-path .

# Web
cd web && npm run lint
cd web && npm run typecheck
cd web && npm run knip
cd web && npm run madge

# Whole-repo (mirrors CI)
lefthook run pre-push --all-files
```
