<!-- Keep the title short (under 70 chars). Use the body for context. -->

## Summary

-

## What changed

-

## Stage / spec link

<!-- Which stage does this map to? See docs/roadmap/. -->
- Stage:

## Test plan

- [ ] `cd api && make lint test build`
- [ ] `cd web && npm run lint && npm run typecheck && npm run build`
- [ ] Migrations apply cleanly twice in a row
- [ ] Bench (`bench/queries.json`) pass rate unchanged or improved
- [ ] Manual: search bar smoke-test in the live UI

## Spec-faithfulness check

- [ ] No deviation from the 7-signals × archetype-weights contract
- [ ] Any alternative formulas are config-switchable, off by default
- [ ] Data-quality calls flagged in the writeup
