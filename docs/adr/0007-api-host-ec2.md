# ADR-0007: API host — AWS EC2 (`c7i.xlarge`, `us-east-1`)

- **Status**: Accepted
- **Date**: 2026-05-30
- **Deciders**: Daniel
- **Supersedes**: the API-host choice in [ADR-0001](0001-stack-choice.md) (Fly.io `iad`). The rest of ADR-0001 — Go API, Supabase Postgres, Next.js on Vercel — stands.

## Context

ADR-0001 put the Go API on Fly.io (`iad`), paired with Supabase (`us-east-1`)
for a ≤10 ms API↔DB hop. Two things changed since then:

- **The embedder went in-process (ADR-0006).** The ORT adapter links native
  `libonnxruntime` (runtime `dlopen`) + `libtokenizers.a` (static CGo). That's a
  bespoke build/runtime image with specific shared libs, not a generic buildpack
  target — whoever runs it, we own the image.
- **The hot path is now CPU-bound on embed.** With the pipeline pool
  (#107) embed throughput scales with vCPUs, so the API box's core count is a
  real capacity lever — and we want to *profile the CGo path under load*, which
  means SSH onto the running box.

Given we already wanted same-region colocation with Supabase, and Supabase runs
on AWS `us-east-1`, hosting the API on AWS in the same region gets the original
≤10 ms hop *more* literally than cross-provider Fly↔Supabase did.

Options weighed for the API host (Supabase/Vercel unchanged):

| Host | SSH / profiling | Native-lib image | Same-region as Supabase | Ops |
|---|---|---|---|---|
| Fly.io `iad` (ADR-0001) | limited | Dockerfile | adjacent, cross-provider | low |
| AWS Lambda | ✗ | awkward (86 MB model load, CGo libs, cold start) | ✓ | low |
| AWS Fargate (ECS) | ✗ (no shell) | ✓ | ✓ | low |
| **AWS EC2** | **✓ full** | ✓ (own the AMI) | ✓ (same region/AZ) | manual |

## Decision

Run the Go API on a single **AWS EC2 `c7i.xlarge` (4 vCPU / 8 GB, Sapphire
Rapids), `us-east-1`**, co-located with Supabase. EC2 over Fargate/Lambda
because the CGo embedder benefits from **SSH access** (live profiling, `perf`,
swapping the ONNX lib) during the trial, and we want full control of the AMI
that carries the native libs.

### Sizing — the capacity model

Per request: **~1 ORT embed (~2–3 ms CPU, pooled)** + **1 same-region Supabase
round-trip (~5–15 ms, I/O — frees the core while it waits)**. So the box's hard
ceiling is embed CPU throughput; SQL waits are cheap goroutines. With the pool
(intra-op = 1, one busy core per in-flight embed), sustained throughput ≈
`vCPUs × ~230 embeds/s` (keeping ~70 % util so p95 stays < 100 ms).

Measured on an 8-core dev box (#107): pool=1 → 233/s (the old mutex ceiling),
pool=4 → **816/s**, pool=8 → 1053/s — near-linear to physical cores, validating
the model.

| EC2 | vCPU / RAM | embed pool | sustained req/s | ≈ concurrent users\* |
|---|---|---|---|---|
| `c7i.large` | 2 / 4 GB | 3 | ~450 | ~750 |
| **`c7i.xlarge`** | **4 / 8 GB** | **4** | **~900** | **~1,500** |
| `c7i.2xlarge` | 8 / 16 GB | 6–8 | ~1,800 | ~3,000 |

\* Model: an actively-typing user fires ~2 req/s (debounced keystrokes) and
~30 % of present users are typing at any instant → ~0.6 req/s each, so
`users ≈ req/s ÷ 0.6`. Readers/idle cost ~0. These are extrapolated ceilings,
not a load test of the live box — confirm with `hey`/`wrk` against the deploy,
and note the co-factor: if the Supabase tier can't keep up with the SQL rate, it
becomes the limit, not the EC2 box.

`c7i.xlarge` is the pick: ~1,500-user headroom, room for a pool of 4, SSH,
~$0.18/hr on-demand — without paying for 8 vCPU the trial won't use. Graviton
(`c7g.xlarge`, ~20 % cheaper/faster) is the price-perf alternative but needs an
ARM rebuild of both native libs; deferred.

## Consequences

**Good**

- SSH onto the box — profile the CGo embedder live, swap the ONNX lib, read
  `perf`. The reason we picked EC2 over Fargate.
- Same AWS region as Supabase → the ≤10 ms API↔DB hop ADR-0001 wanted, now
  intra-region rather than cross-provider.
- vCPUs are usable: the embed pool (#107) turns cores into throughput.
- Full control of the AMI that carries `libonnxruntime` + `libtokenizers`.

**Bad / cost**

- Manual box ops: we own the AMI, the systemd unit, TLS termination, and
  patching — no buildpack does it for us. Captured in the deploy runbook.
- Single box = no HA in V1. Acceptable for a trial; an ASG + ALB is the path if
  it ever matters.
- Right-sizing is on us (the table above is the guide).

**Revisit when**

- Load needs HA/elasticity → Auto Scaling Group behind an ALB, or containerize
  to ECS (the image work is already done).
- If SSH/profiling stops earning its keep post-trial, Fargate drops the ops
  burden for the same image.
