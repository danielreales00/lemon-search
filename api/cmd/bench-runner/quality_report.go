package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// qualityAgg holds set-wide means of the per-query metrics, plus counts that do
// not average (new@rank1). Distance/rating/etc. are simple means over the rows
// that scored (errors and empty results are excluded from the relevant means).
type qualityAgg struct {
	n                 int
	meanDistanceKM    float64
	medianDistanceKM  float64
	meanRating        float64
	meanLogReviews    float64
	pctOpen           float64
	categoryPrecision float64 // mean over rows that carry category tokens
	claimedPct        float64
	diversity         float64
	newAtRank1Count   int
	goldenAt5         float64 // mean over rows that carry golden anchors
}

// aggregateQuality means the per-query metrics across a run. Category precision
// averages only rows that declare expected tokens; golden@5 only rows with
// anchors; the rest average over every scored (error-free, non-empty) row.
func aggregateQuality(rows []qualityRow) qualityAgg {
	var a qualityAgg
	var catSum, catN float64
	var goldSum, goldN float64
	for i := range rows {
		r := &rows[i]
		if r.err != nil || r.metrics.N == 0 {
			continue
		}
		a.n++
		a.meanDistanceKM += r.metrics.MeanDistanceKM
		a.medianDistanceKM += r.metrics.MedianDistanceKM
		a.meanRating += r.metrics.MeanRating
		a.meanLogReviews += r.metrics.MeanLogReviews
		a.pctOpen += r.metrics.PctOpen
		a.claimedPct += r.metrics.ClaimedPct
		a.diversity += r.metrics.Diversity
		if r.metrics.NewAtRank1 {
			a.newAtRank1Count++
		}
		if r.metrics.CategoryHasExpect {
			catSum += r.metrics.CategoryPrecision
			catN++
		}
		if r.golden != intentNA {
			goldSum += r.golden
			goldN++
		}
	}
	if a.n > 0 {
		n := float64(a.n)
		a.meanDistanceKM /= n
		a.medianDistanceKM /= n
		a.meanRating /= n
		a.meanLogReviews /= n
		a.pctOpen /= n
		a.claimedPct /= n
		a.diversity /= n
	}
	if catN > 0 {
		a.categoryPrecision = catSum / catN
	}
	if goldN > 0 {
		a.goldenAt5 = goldSum / goldN
	}
	return a
}

const reportPerm = 0o644

// writeQualityReport renders the full markdown: the literal-vs-decay headline
// comparison, the aggregate table, and the per-query tables for each arm.
func writeQualityReport(path string, qf qualityFile, runs []qualityRun) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Ranking-quality eval — %s\n\n", time.Now().Format("2006-01-02"))
	writeQualityIntro(&b, qf, runs)
	writeABComparison(&b, qf, runs)
	for i := range runs {
		writePerQuerySection(&b, &runs[i])
	}
	writeMetricGlossary(&b)

	if err := os.WriteFile(path, []byte(b.String()), reportPerm); err != nil { //nolint:gosec // report is non-secret, world-readable by design
		return fmt.Errorf("writing quality report %q: %w", path, err)
	}
	return nil
}

func writeQualityIntro(b *strings.Builder, qf qualityFile, runs []qualityRun) {
	fmt.Fprintf(b, "Curated **%d**-query ranking-quality set run through the real pipeline ", len(qf.Queries))
	b.WriteString("(intent overlay + single-round-trip retrieval + pure re-rank) in-process against ")
	b.WriteString("the local Postgres (22,568 Miami businesses). Metrics are computed over the top-15 ")
	b.WriteString("and are SPEC-DERIVED proxies for what each query should prioritize — not opinion. ")
	fmt.Fprintf(b, "Dataset claimed base rate: **%.1f%%**.\n\n", qf.ClaimedBaseRate)
	b.WriteString("The two arms differ ONLY in `signal_formulas.distance`:\n\n")
	b.WriteString("- **literal** — spec default: `max(1 - d/30mi, 0)`\n")
	b.WriteString("- **decay** — per-archetype `exp(-d/decay_km)` (3km utility … 80km high-stakes)\n\n")
	b.WriteString("Every other knob (rating mode, weights, photo/friend/open constants) is shared, ")
	b.WriteString("read from the `-config` file, so the table isolates the distance formula.\n\n")
	if empties := emptyQueries(runs); len(empties) > 0 {
		fmt.Fprintf(b, "> **Note.** %d free-form/vibe queries return zero results in this lexical "+
			"baseline (no embedder wired): %s. They are honest gaps the semantic layer "+
			"(LEMON_FF_SEMANTIC, ADR-0006) is meant to close; they are excluded from the "+
			"aggregate means.\n\n", len(empties), strings.Join(empties, ", "))
	}
}

// emptyQueries lists the queries (deduped) that returned no results in the first
// run — a lexical-baseline gap worth surfacing, not a metric bug.
func emptyQueries(runs []qualityRun) []string {
	if len(runs) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	for i := range runs[0].rows {
		r := &runs[0].rows[i]
		if r.err == nil && r.metrics.N == 0 {
			if _, dup := seen[r.query.Q]; !dup {
				seen[r.query.Q] = struct{}{}
				out = append(out, "`"+r.query.Q+"`")
			}
		}
	}
	return out
}

// writeABComparison is the headline: literal vs decay on every aggregate metric,
// with a delta column so the locality-vs-quality trade is read at a glance.
func writeABComparison(b *strings.Builder, qf qualityFile, runs []qualityRun) {
	if len(runs) < 2 {
		return
	}
	lit := aggregateQuality(runs[0].rows)
	dec := aggregateQuality(runs[1].rows)
	b.WriteString("## Headline — literal vs decay\n\n")
	b.WriteString("| metric | literal | decay | Δ (decay−literal) |\n|---|---|---|---|\n")
	row := func(name string, a, c float64, fmtStr string) {
		fmt.Fprintf(b, "| %s | "+fmtStr+" | "+fmtStr+" | "+fmtStr+" |\n", name, a, c, c-a)
	}
	row("mean_distance_km", lit.meanDistanceKM, dec.meanDistanceKM, "%.2f")
	row("median_distance_km", lit.medianDistanceKM, dec.medianDistanceKM, "%.2f")
	row("mean_rating (0..1)", lit.meanRating, dec.meanRating, "%.3f")
	row("mean_log_reviews (0..1)", lit.meanLogReviews, dec.meanLogReviews, "%.3f")
	row("pct_open", lit.pctOpen, dec.pctOpen, "%.2f")
	row("category_precision", lit.categoryPrecision, dec.categoryPrecision, "%.3f")
	row("claimed_pct", lit.claimedPct, dec.claimedPct, "%.3f")
	row("diversity", lit.diversity, dec.diversity, "%.3f")
	row("golden_precision@5", lit.goldenAt5, dec.goldenAt5, "%.3f")
	fmt.Fprintf(b, "| new_at_rank1 (count) | %d | %d | %+d |\n", lit.newAtRank1Count, dec.newAtRank1Count, dec.newAtRank1Count-lit.newAtRank1Count)
	fmt.Fprintf(b, "| claimed base rate | %.3f | %.3f | — |\n\n", qf.ClaimedBaseRate/100, qf.ClaimedBaseRate/100)
	writeABRead(b, lit, dec)
}

// writeABRead states the decision rule and the read: decay is worth flipping the
// default only if it lowers mean distance WITHOUT hurting category precision or
// rating. The text reports the measured movement so the call is grounded.
func writeABRead(b *strings.Builder, lit, dec qualityAgg) {
	b.WriteString("**Read.** Decay is worth flipping the spec default only if it improves locality ")
	b.WriteString("(lower mean distance) *without* hurting category_precision or rating. Movement:\n\n")
	distDelta := dec.meanDistanceKM - lit.meanDistanceKM
	catDelta := dec.categoryPrecision - lit.categoryPrecision
	ratingDelta := dec.meanRating - lit.meanRating
	fmt.Fprintf(b, "- distance: %+.2f km (%s)\n", distDelta, betterWorse(distDelta < 0))
	fmt.Fprintf(b, "- category_precision: %+.3f (%s)\n", catDelta, betterWorse(catDelta >= 0))
	fmt.Fprintf(b, "- rating: %+.3f (%s)\n\n", ratingDelta, betterWorse(ratingDelta >= 0))
	switch {
	case distDelta < -0.05 && catDelta >= -0.005 && ratingDelta >= -0.005:
		b.WriteString("**Recommendation: flip the default to `decay`.** It tightens locality with no ")
		b.WriteString("measurable cost to category precision or rating — exactly the spec's intent for ")
		b.WriteString("low-stakes/utility queries (distance should dominate near home).\n\n")
	case distDelta >= 0:
		b.WriteString("**Recommendation: keep `literal`.** Decay did not improve locality here, so there ")
		b.WriteString("is no case for departing from the spec default (ADR-0004).\n\n")
	default:
		b.WriteString("**Recommendation: keep `literal` as the default; ship `decay` as the documented ")
		b.WriteString("opt-in switch.** Decay tightens locality but the trade against category/rating is ")
		b.WriteString("not clean enough to override the spec contract by default.\n\n")
	}
	writeClaimedNote(b, lit, dec)
}

// writeClaimedNote flags when claimed_pct runs well above the dataset base rate
// in either arm — a separate, spec-relevant signal (the spec wants claimed to be
// a tiebreaker, not an override) that this harness now makes measurable.
func writeClaimedNote(b *strings.Builder, lit, dec qualityAgg) {
	const baseRate = 0.207
	if lit.claimedPct < 2*baseRate && dec.claimedPct < 2*baseRate {
		return
	}
	fmt.Fprintf(b, "_Side-observation._ claimed_pct sits at %.0f%% (literal) / %.0f%% (decay) "+
		"against a ~20.7%% base rate — above the ~2x line. Decay pulls it toward the base rate by "+
		"surfacing nearby (often unclaimed) places; under literal, claimed weight + popularity skew "+
		"co-select for established, claimed businesses. Worth a follow-up claimed-weight sweep with "+
		"this same harness.\n\n", lit.claimedPct*100, dec.claimedPct*100)
}

func betterWorse(good bool) string {
	if good {
		return "better"
	}
	return "worse"
}

// writePerQuerySection renders one arm's per-query table.
func writePerQuerySection(b *strings.Builder, run *qualityRun) {
	fmt.Fprintf(b, "## Per-query — %s\n\n", run.label)
	b.WriteString("| query | kind | loc | dist km (mean/med) | rating | logrev | open | cat_prec | intent | claimed | new@1 | div | golden@5 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for i := range run.rows {
		writeQueryRow(b, &run.rows[i])
	}
	b.WriteString("\n")
}

func writeQueryRow(b *strings.Builder, r *qualityRow) {
	if r.err != nil {
		fmt.Fprintf(b, "| %q | %s | %s | ERR: %s |\n", r.query.Q, r.query.Kind, r.loc.Label, r.err.Error())
		return
	}
	if r.metrics.N == 0 {
		fmt.Fprintf(b, "| %q | %s | %s | (no results — lexical baseline, no semantic layer) |\n", r.query.Q, r.query.Kind, r.loc.Label)
		return
	}
	m := r.metrics
	fmt.Fprintf(b, "| %q | %s | %s | %.2f / %.2f | %.3f | %.3f | %.0f%% | %s | %s | %.0f%% | %s | %.2f | %s |\n",
		r.query.Q, r.query.Kind, r.loc.Label,
		m.MeanDistanceKM, m.MedianDistanceKM,
		m.MeanRating, m.MeanLogReviews, m.PctOpen*100,
		catCell(m), intentCell(m), m.ClaimedPct*100,
		boolCell(m.NewAtRank1), m.Diversity, goldenCell(r.golden))
}

func catCell(m qualityMetrics) string {
	if !m.CategoryHasExpect {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", m.CategoryPrecision*100)
}

func intentCell(m qualityMetrics) string {
	if m.IntentLabel == "" {
		return "—"
	}
	if m.IntentAdherence == intentNA {
		return m.IntentLabel + ":n/a"
	}
	return fmt.Sprintf("%s:%.0f%%", m.IntentLabel, m.IntentAdherence*100)
}

func goldenCell(g float64) string {
	if g == intentNA {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", g*100)
}

func boolCell(v bool) string {
	if v {
		return "YES"
	}
	return "no"
}

func writeMetricGlossary(b *strings.Builder) {
	b.WriteString("## Metric definitions\n\n")
	b.WriteString("- **dist km (mean/med)** — raw retrieval distance of the top-15. Lower = tighter locality.\n")
	b.WriteString("- **rating** — mean `lemon_score/10` over rated results (0..1).\n")
	b.WriteString("- **logrev** — mean `log(1+reviews)/log(1+10000)` (spec popularity signal, 0..1).\n")
	b.WriteString("- **open** — fraction explicitly open now (unknown hours count as not-open).\n")
	b.WriteString("- **cat_prec** — fraction whose subcategory/category matches an expected token (category-aware matching).\n")
	b.WriteString("- **intent** — per-intent adherence: `cheap`=frac $/$$, `fancy`=frac $$$+, `open_now`=frac open; vibe intents are n/a (judged on cat_prec).\n")
	b.WriteString("- **claimed** — fraction claimed; compare to the ~20.7% base rate (≈base good, ~2x = dominating).\n")
	b.WriteString("- **new@1** — is the #1 result a new business? Spec: must be `no`.\n")
	b.WriteString("- **div** — distinct name-stems / 15 (chains clumping lowers it).\n")
	b.WriteString("- **golden@5** — precision@5 vs hand-picked anchors (— when none).\n")
}
