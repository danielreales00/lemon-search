# Feature flags

Flags exist for **one reason**: let incomplete work merge to `main` without
affecting the deployed app, so parallel agents can ship small PRs continuously
while `main` stays demoable. They are temporary by design.

## Rules

1. **Default off in prod.** A flag's prod default is `false` until the feature
   is complete, tested, and signed off.
2. **Register every flag here** (table below) with purpose, default, owner, and
   removal condition.
3. **Delete flags promptly.** Once a feature is on-by-default everywhere, remove
   the flag and the dead branch in the same PR. No permanent flags — they rot.
4. **Flags gate incomplete work, not tuning.** Tuning knobs for *complete*
   features (which rating formula, which distance curve) live in
   `config/ranking.yaml` under `signal_formulas`, not here.

## Conventions

### Backend (Go)

Env var `LEMON_FF_<NAME>`, read once at startup into `internal/flags`:

```go
// api/internal/flags/flags.go
package flags

import "os"

type Flags struct {
    Intent bool // LEMON_FF_INTENT — gate intent extractor while lexicon is partial
}

func Load() Flags {
    return Flags{
        Intent: os.Getenv("LEMON_FF_INTENT") == "true",
    }
}
```

Consume the struct; never read `os.Getenv` for a flag deep in the call tree.
The flag is decided once and passed down.

### Frontend (Next.js)

Env var `NEXT_PUBLIC_FF_<NAME>` (must be `NEXT_PUBLIC_` to reach the client):

```ts
// web/lib/flags.ts
export const flags = {
  intent: process.env.NEXT_PUBLIC_FF_INTENT === 'true',
} as const;
```

### Where defaults live

| Environment | Source |
|---|---|
| Local dev | `.env.local` (copy from `.env.example`) |
| Fly.io (API prod) | `flyctl secrets set LEMON_FF_INTENT=...` |
| Vercel (FE prod/preview) | Vercel env vars |
| CI | unset → all flags `false` (tests must pass with flags off) |

Tests run with flags off by default; a feature's tests explicitly enable its
flag where needed.

## Registry

| Flag | Layer | Purpose | Prod default | Owner | Remove when |
|---|---|---|---|---|---|
| `LEMON_FF_INTENT` | api | Gate the intent extractor while the lexicon is incomplete (Stage 3) | `false` | — | Stage 3 lexicon complete + bench ≥80% |
| `NEXT_PUBLIC_FF_INTENT` | web | Show intent-derived UI hints (e.g., "showing open now") | `false` | — | Same as above |

_(Add rows as flags are introduced. Empty until the first flagged feature lands.)_

## Anti-patterns

- A flag that's been `true` in prod for a while with no removal PR → delete it.
- A flag read in 5 different places → read once, pass the value.
- Using a flag to toggle between two *complete* behaviors → that's config
  (`config/ranking.yaml`), not a feature flag.
- Branching tests on an env var read at import time → inject the flag value
  instead so tests are deterministic.

## Cross-references

- Workflow that relies on flags for parallel safety: [workflow.md](workflow.md)
- Tuning switches (not flags): `config/ranking.yaml`, `docs/ranking/semantics.md`
