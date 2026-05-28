# Workflow — chunked work, worktrees, parallel agents

How we turn a roadmap stage into mergeable units that several agents can build
in parallel without stepping on each other. CLAUDE.md has the summary; this is
the detail.

## The unit of work

**One chunk = one board issue = one branch = one worktree = one PR.**

A chunk is small enough that:

- It fits in one PR (Size `XS`–`M`; rarely `L`).
- It touches a bounded set of files, named up front.
- It honors a known contract (C1–C8 in
  [../roadmap/05-architectural-contracts.md](../roadmap/05-architectural-contracts.md))
  so it composes with parallel work rather than colliding.

If a roadmap sub-task is bigger than `M`, split it before starting.

## The GitHub project board

Project: <https://github.com/users/danielreales00/projects/2>
(`PVT_kwHOA--NDs4BY_nZ`).

### Fields

| Field | Type | Values |
|---|---|---|
| Status | single-select | `Backlog` → `Ready` → `In progress` → `In review` → `Done` |
| Priority | single-select | `P0`, `P1`, `P2` |
| Size | single-select | `XS`, `S`, `M`, `L`, `XL` |
| Estimate | number | optional hours |

### Status semantics

| Status | Meaning |
|---|---|
| `Backlog` | Captured, not yet scoped/ready |
| `Ready` | Scoped, unblocked, can be claimed |
| `In progress` | An agent owns it; branch + worktree exist |
| `In review` | PR open, CI running/green, awaiting merge |
| `Done` | Merged to `main`, worktree removed |

### Managing the board from the CLI

```bash
# List items
gh project item-list 2 --owner danielreales00

# Create an issue and add it to the board
gh issue create --repo danielreales00/lemon-search \
  --title "feat(intent): price-family lexicon entries" \
  --body "Implements the price family (cheap/fancy/...) per docs/ranking/intent.md. Honors C5 Overlay. Touches api/internal/intent/lexicon.go + tests." \
  --label stage-3
gh project item-add 2 --owner danielreales00 --url <issue-url>

# Move an item's Status (needs the project + field + option IDs; see below)
gh project item-edit --id <item-id> --project-id PVT_kwHOA--NDs4BY_nZ \
  --field-id <Status-field-id> --single-select-option-id <option-id>
```

Field/option IDs are stable; capture them once:

```bash
gh project field-list 2 --owner danielreales00 --format json
```

## Branch + worktree

Branch name mirrors the commit scope:

```
<type>/<scope>-<slug>
# e.g. feat/intent-price-lexicon, fix/ingest-escaped-quotes, data/schema-friend-count
```

Each agent works in its own worktree so parallel work never shares a working
directory:

```bash
# from the main checkout
git worktree add ../lemon-search-intent-price -b feat/intent-price-lexicon
cd ../lemon-search-intent-price
# ... build, commit, push ...
gh pr create --fill --base main
# after merge:
cd ../lemon-search
git worktree remove ../lemon-search-intent-price
git worktree prune
```

Worktrees are sibling dirs named `../lemon-search-<slug>`. They share the same
`.git`, so branches and objects are common; only the working tree is isolated.

### Spawning a parallel agent (harness)

When dispatching an Agent to own a chunk, use worktree isolation so it gets its
own copy:

```
Agent(
  description: "<scope>: <slug>",
  isolation: "worktree",
  prompt: "Implement board issue #NN. Read CLAUDE.md + docs/roadmap/<stage>.md +
           the relevant contract (Cx). Touch ONLY <files>. Honor the spec
           contract. Open a PR against main when CI-equivalent checks pass."
)
```

The harness creates and cleans up the worktree. Brief each agent with: the
issue, the files it owns, the contract it honors, and the explicit
instruction not to touch files outside its chunk.

## Avoiding collisions between parallel agents

- **Partition by package.** Two agents on `internal/rank` and `internal/intent`
  won't conflict; two agents both editing `internal/api/search.go` will.
  Chunk along package boundaries.
- **Code against contracts, not against in-flight work.** If chunk B needs a
  type chunk A is adding, define that type in `domain` first (a tiny
  contract-only PR), merge it, then both build against it.
- **Shared files get a single owner.** `config/ranking.yaml`,
  `supabase/migrations/*`, `web/app/page.tsx` — if multiple chunks need them,
  sequence those chunks or assign one agent to integrate.
- **Migrations are append-only and numbered.** Two agents adding migrations
  must coordinate numbers (`0003_`, `0004_`); never reuse a number.

## Feature flags (keep `main` deployable)

Detail + registry: [feature-flags.md](feature-flags.md).

Rule: if a chunk can't land complete-and-correct in one PR, gate it behind a
flag, default **off** in prod. This lets half-built features merge to `main`
without affecting the deployed app, which is what makes parallel agents safe.

- Backend: `LEMON_FF_<NAME>` → `internal/flags`.
- Frontend: `NEXT_PUBLIC_FF_<NAME>`.
- Register the flag; delete it once the feature is permanently on.

## PR conventions

- Title is a conventional-commit header (`type(scope): subject`).
- Body uses `.github/PULL_REQUEST_TEMPLATE.md`: link the board item + stage,
  fill the test plan and the spec-faithfulness checklist.
- One logical change per PR. If the diff sprawls, split it.
- CI must be green. Never merge red; never force-push `main`.
- Squash-merge keeps `main` history one-commit-per-chunk (matches the
  board-item granularity).

## Dependabot

12 dependency PRs are open (`gh pr list`). Triage rules:

- **Patch/minor, CI green** → merge.
- **Major bumps** (TypeScript 6, eslint-plugin-unicorn 64, boundaries 6, etc.)
  → check the changelog; they may need config/code updates. Don't blanket-merge.
- Group related lint-plugin bumps into one validation pass so we only refit the
  ESLint config once.

## End-to-end example

Chunk: "intent price-family lexicon."

```bash
# 1. Issue on the board, Status: Ready → In progress
gh issue create --repo danielreales00/lemon-search \
  --title "feat(intent): price-family lexicon entries"
gh project item-add 2 --owner danielreales00 --url <url>

# 2. Worktree
git worktree add ../lemon-search-intent-price -b feat/intent-price-lexicon
cd ../lemon-search-intent-price

# 3. Build behind LEMON_FF_INTENT (already off in prod), with tests
#    edit api/internal/intent/lexicon.go (+ _test.go)

# 4. Verify
cd api && go test ./internal/intent/... && golangci-lint run ./internal/intent/...

# 5. PR
gh pr create --fill --base main      # board item → In review

# 6. Merge (green CI) → board item Done → cleanup
git worktree remove ../lemon-search-intent-price
```

## Cross-references

- Contracts that keep parallel work composable: [../roadmap/05-architectural-contracts.md](../roadmap/05-architectural-contracts.md)
- Feature-flag registry: [feature-flags.md](feature-flags.md)
- Quality gates a PR must pass: [../development.md](../development.md)
- Deploy: [deployment.md](deployment.md)
