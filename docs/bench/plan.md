# Load-bench plan

How we prove the speed claims (sub-100ms p95) under concurrency, and where the
system's real ceiling is. Two benches with different jobs; one harness
(`api/cmd/loadbench`) that captures end-to-end latency **and** the server's stage
timings so a degrading p95 can be *attributed* rather than guessed at.

Companion to [ADR-0007](../adr/0007-api-host-ec2.md) (host + capacity model) and
the correctness bench in [`../../bench/README.md`](../../bench/README.md).

## The two benches

| | Grading bench | Ceiling bench |
|---|---|---|
| **Question** | Is p95 < 100ms at realistic concurrency? | Where's the throughput knee, and what's the wall? |
| **Load** | 20–50 concurrent users (~25–60 rps) | rate ramp to saturation (→ 800+ rps) |
| **Supabase** | Small (2 vCPU) — what we run in prod | temporarily 2XL–4XL (8–16 vCPU), then scaled back |
| **Cost** | ~free | a few $ of hourly compute, torn down after |
| **Deliverable** | "p95 = X ms < 100ms ✅" | throughput curve + honest co-scaling story |

The grading bench is what the spec actually grades. The ceiling bench is the
credibility flex for the writeup's speed section — *measured*, with the
EC2↔Supabase asymmetry named, not hand-waved.

## What a request actually costs (measured)

Warm, single-query, on the loaded local DB (22,568 rows; Docker Postgres on a
shared dev box — constants differ on dedicated Supabase cores, magnitudes hold):

| query type | warm DB time |
|---|---|
| `coffee` (common) | ~17 ms |
| `bar` (worst case, 3,407 matches) | ~28–31 ms |
| `co` (short prefix) | ~30 ms |
| `require_open` (over-fetch ×5) | ~40 ms |
| raw HNSW vector probe | **~1 ms** |

Two facts that drive everything: a query is **~20–30 ms of DB CPU**, and
**semantic recall is nearly free** (~1 ms HNSW) — the cost is the lexical rank +
open-status, not the vectors. The embed itself is ~2 ms of API-box CPU (pooled,
[#107](https://github.com/danielreales00/lemon-search/pull/107)).

## Why Supabase is the bottleneck, and how to size it

At ~25 ms DB CPU/query, **one core ≈ 40 queries/sec**. To match the EC2 box's
~800 req/s the DB needs ~20 cores in flight. The Supabase gotcha:

> Micro/Small/Medium/Large are **all 2 vCPU** (they differ only in RAM, and
> Micro/Small are *burstable* — they throttle under sustained load). CPU scales
> only at **XL (4) → 2XL (8) → 4XL (16) → 8XL (32)**.

So the ceiling bench needs **2XL–4XL** compute for the run window. Compute is
billed hourly — spin up, run the sweep, scale back to Small. Per-tier rough
throughput (at ~25 ms/query):

| Supabase compute | vCPU | ~max sustained rps |
|---|---|---|
| Small / Medium / Large | 2 | ~80 |
| XL | 4 | ~160 |
| 2XL | 8 | ~320 |
| 4XL | 16 | ~640 |

A live preview of this exact dynamic — `loadbench` against the dev box (2
effective Postgres cores), 5s/rate:

```
rate  25 → wall p95   70ms | sql  67  embed  2          ✅
rate  50 → wall p95   71ms | sql  66  embed  2          ✅
rate 100 → wall p95  169ms | sql 157  embed  8          ⚠️ knee
rate 200 → wall p95 1837ms | sql 1749 embed 27 | 503s   💥 DB saturates
```

`sql` is the wall at every step; `embed` never breaks a sweat. The knee sits
right where the 2-core math predicts (~80 rps). On a co-scaled Supabase the knee
moves out proportionally to cores, and the *next* wall becomes the EC2 embed
pool (~800 rps on `c7i.xlarge`).

## The harness — `api/cmd/loadbench`

Open-loop (constant arrival rate, not closed-loop concurrency): requests are
scheduled at fixed instants `start + i/rate` regardless of in-flight count, and
latency is measured from each request's **intended** send time. That surfaces
coordinated omission — a stalled server shows blown-up tail latency instead of
silently throttling offered load. A bounded in-flight cap prevents OOM; requests
it can't admit are counted as `dropped` (a saturation signal). Per request it
records wall latency and the server's `embed_ms`/`sql_ms`/`rerank_ms`/`total_ms`,
so every row attributes the cost.

```bash
cd api && go run ./cmd/loadbench \
  -base-url http://<api-host>:8080 \
  -rates 25,50,100,200,400,800 \
  -duration 30s -warmup 5s \
  -out ../bench/load-results-$(date +%F).json
```

Output: a per-rate table (wall p50/p95/p99, server p95, sql/embed p95, errors,
drops) to stdout plus a JSON artifact. The wrapper `scripts/loadbench/run-sweep.sh`
fills in the dated `-out` and sane defaults; `scripts/loadbench/emit-vegeta-targets.sh`
renders the same corpus as a [vegeta](https://github.com/tsenart/vegeta) target
file for an independent cross-check of the pure latency numbers.

The corpus (`bench/load-corpus.json`) is weighted for search-as-you-type: short
prefixes dominate (every debounced keystroke), category terms are common,
vibe/intent phrases are the expensive-but-interesting minority. Each sampled
query is paired with a random Miami point + time-of-day so plans don't collapse
into one cached shape.

## Procedure (ceiling bench)

1. **Topology**: API on the EC2 box (ORT binary, `LEMON_EMBED_POOL_SIZE=4`,
   semantic+intent flags on) + Supabase `us-east-1`, data ingested + embedded +
   `ANALYZE`d. Run `loadbench` from a **second in-region EC2** — never your
   laptop (you'd measure home-internet RTT, not the system).
2. **Scale Supabase up** to 2XL–4XL for the window.
3. **Tune connections**: `LEMON_DB_MAX_CONNS` ~25–40 (≈ in-flight queries at the
   target rate), under the tier's `max_connections`, or front with Supavisor.
4. **Sweep**: warmup primes caches, then ramp the rates above. The capacity
   number is the rate where **p95 crosses 100 ms** (the knee).
5. **Watch attribution**: `loadbench`'s sql/embed split + the EC2 per-core CPU +
   the Supabase dashboard (CPU%, IOPS, connections, cache-hit). Name the wall.
6. **Scale Supabase back down**; commit `bench/load-results-<date>.json` + a
   short writeup section (curve, knee, attribution).

## What we report

- Grading: p95 (wall + server) at realistic concurrency vs the 100 ms budget.
- Ceiling: the throughput curve, the knee rps, the stage that walls first, and
  the co-scaling path (Supabase cores ↔ rps). Honesty over a single big number.
