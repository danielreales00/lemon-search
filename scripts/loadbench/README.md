# loadbench scripts

Open-loop load testing for the `/search` endpoint. Full methodology +
sizing math: [`docs/bench/plan.md`](../../docs/bench/plan.md).

- **`run-sweep.sh`** — wrapper around `api/cmd/loadbench`; runs a rate ramp and
  writes `bench/load-results-<date>.json`. Env knobs: `BASE_URL`, `RATES`,
  `DURATION`, `WARMUP`, `TIMEOUT`, `OUT`.
- **`emit-vegeta-targets.sh`** — renders `bench/load-corpus.json` as a
  [vegeta](https://github.com/tsenart/vegeta) target file (needs `jq`), for an
  independent cross-check of the latency numbers.

The Go harness (`api/cmd/loadbench`) is the source of truth: unlike vegeta it
parses the server's `embed_ms`/`sql_ms`/`rerank_ms` so every rate attributes its
latency to the API box vs the database.

```bash
# Local smoke
./run-sweep.sh

# Against the deployed box, from a same-region load box (not your laptop)
BASE_URL=http://<api-host>:8080 RATES=25,50,100,200,400,800 DURATION=30s ./run-sweep.sh

# vegeta cross-check
./emit-vegeta-targets.sh http://<api-host>:8080 > /tmp/targets.txt
vegeta attack -targets /tmp/targets.txt -rate 200 -duration 30s | vegeta report
```
