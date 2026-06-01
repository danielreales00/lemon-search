package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/search"
)

// qualityFile is the curated ranking-quality query set (bench/quality-queries.json).
// Unlike the findability bench it carries per-query expectations (category tokens,
// archetype, intent label, golden anchors) used to compute spec-derived metrics.
type qualityFile struct {
	UserLocation    geoPoint       `json:"user_location"`
	NowOverride     string         `json:"now_override"`
	ClaimedBaseRate float64        `json:"claimed_base_rate_pct"`
	Queries         []qualityQuery `json:"queries"`
}

type geoPoint struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label string  `json:"label"`
}

// qualityQuery is one ranking-quality probe. CategoryMatch holds lowercase
// substrings tested against each result's subcategory+category; ExpectedArch is
// the spec's demand-shape bucket; Intent tags the price/time/vibe intention;
// GoldenTop5 are hand-picked anchors expected in the top-5.
type qualityQuery struct {
	Kind          string    `json:"kind"`
	Q             string    `json:"q"`
	CategoryMatch []string  `json:"category_match"`
	ExpectedArch  string    `json:"expected_archetype"`
	Intent        string    `json:"intent"`
	GoldenTop5    []string  `json:"golden_top5"`
	LocOverride   *geoPoint `json:"user_location_override"`
}

// qualityRow pairs a query with the metrics it produced under one config.
type qualityRow struct {
	query   qualityQuery
	loc     geoPoint
	metrics qualityMetrics
	golden  float64 // precision@5 vs golden anchors; intentNA when no anchors
	err     error
}

// qualityRun is the full set of rows for one configuration (one distance mode).
type qualityRun struct {
	label string
	rows  []qualityRow
}

// runQuality is the -quality mode entrypoint. It loads the curated set, runs it
// through the real pipeline under the spec-literal distance mode and the decay
// mode (the A/B headline), and writes the markdown report. cfg supplies every
// non-distance knob from the operator-supplied -config file, so the harness
// doubles as a config A/B + tuning rig.
func runQuality(ctx context.Context, cfg *config.Ranking, repo domain.BusinessRepo, o opts) error {
	qf, err := loadQuality(o.qualityBench)
	if err != nil {
		return err
	}
	now, err := time.Parse(time.RFC3339, qf.NowOverride)
	if err != nil {
		return fmt.Errorf("parsing now_override %q: %w", qf.NowOverride, err)
	}

	// Two configs: spec-literal distance (the default) vs per-archetype decay.
	// Every other knob (rating mode, weights) comes from the loaded cfg, so the
	// only thing that moves between arms is the distance formula.
	literal := *cfg
	literal.SignalFormulas.Distance = distanceLiteral
	decay := *cfg
	decay.SignalFormulas.Distance = distanceDecay

	runs := []qualityRun{
		runQualitySet(ctx, "literal", &literal, repo, qf, now),
		runQualitySet(ctx, "decay", &decay, repo, qf, now),
	}

	if err := writeQualityReport(o.qualityOut, qf, runs); err != nil {
		return err
	}
	printQualitySummary(o.qualityOut, qf, runs)
	return nil
}

// runQualitySet runs every query through a search.Service built on the given
// config and collects the per-query metrics. Intent is ON so the harness
// measures the production overlay + categorical-pin path.
func runQualitySet(ctx context.Context, label string, cfg *config.Ranking, repo domain.BusinessRepo, qf qualityFile, now time.Time) qualityRun {
	svc := search.New(benchLogger(), repo, cfg, true, nil)
	rows := make([]qualityRow, 0, len(qf.Queries))
	for i := range qf.Queries {
		rows = append(rows, runQualityOne(ctx, svc, qf, now, qf.Queries[i]))
	}
	return qualityRun{label: label, rows: rows}
}

// runQualityOne runs a single query (honoring its per-query location override)
// and computes its metrics. A per-query error is carried on the row, not
// returned, so the whole set always produces a report.
func runQualityOne(ctx context.Context, svc *search.Service, qf qualityFile, now time.Time, q qualityQuery) qualityRow {
	loc := qf.UserLocation
	if q.LocOverride != nil {
		loc = *q.LocOverride
	}
	opts := domain.SearchOpts{Lat: loc.Lat, Lng: loc.Lng, Now: now}
	ranked, _, err := svc.Search(ctx, q.Q, opts)
	if err != nil {
		return qualityRow{query: q, loc: loc, err: fmt.Errorf("search %q: %w", q.Q, err)}
	}
	return qualityRow{
		query:   q,
		loc:     loc,
		metrics: computeMetrics(q, ranked),
		golden:  goldenPrecisionAt5(q.GoldenTop5, ranked),
	}
}

func loadQuality(path string) (qualityFile, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied bench path
	if err != nil {
		return qualityFile{}, fmt.Errorf("reading quality bench %s: %w", path, err)
	}
	var qf qualityFile
	if err := json.Unmarshal(b, &qf); err != nil {
		return qualityFile{}, fmt.Errorf("parsing quality bench %s: %w", path, err)
	}
	if len(qf.Queries) == 0 {
		return qualityFile{}, fmt.Errorf("quality bench %s has no queries", path)
	}
	return qf, nil
}

// printQualitySummary echoes the headline (literal vs decay aggregate distance +
// category precision) to stdout. This is a dev CLI, so stdout is the channel.
func printQualitySummary(out string, qf qualityFile, runs []qualityRun) {
	for _, r := range runs {
		agg := aggregateQuality(r.rows)
		fmt.Printf("quality[%s]: mean_dist=%.2fkm cat_prec=%.0f%% rating=%.3f claimed=%.0f%% (base %.1f%%) new@1=%d golden@5=%.0f%%\n", //nolint:forbidigo // bench CLI stdout
			r.label, agg.meanDistanceKM, agg.categoryPrecision*100, agg.meanRating,
			agg.claimedPct*100, qf.ClaimedBaseRate, agg.newAtRank1Count, agg.goldenAt5*100)
	}
	fmt.Printf("wrote quality report: %s\n", out) //nolint:forbidigo // bench CLI stdout
}
