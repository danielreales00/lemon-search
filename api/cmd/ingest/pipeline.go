package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"

	"github.com/danielreales00/lemon-search/api/internal/ingest"
)

// stats is the end-of-run tally the producer accumulates while streaming rows.
type stats struct {
	read            int
	droppedGeo      int
	droppedNoAddr   int
	droppedCatEmpty int
	bucketedOther   int
	loaded          int
	claimedTrue     int
	friendNonzero   int
	err             error
}

// producer runs the pure pipeline stages (sanitize → geo → taxonomy → synth)
// over the parser's output, emitting fully-prepared Business rows on a channel
// and tallying the end-of-run counts. It is the only place that composes the
// stages; the loader just drains the channel.
type producer struct {
	parser *ingest.Parser
	logger *slog.Logger
	done   chan stats
}

func newProducer(parser *ingest.Parser, logger *slog.Logger) *producer {
	return &producer{parser: parser, logger: logger, done: make(chan stats, 1)}
}

// run streams records from the parser, processes each, and sends survivors to
// out. It always closes out (so the loader's range terminates) and publishes
// the final stats exactly once. A parse error on a single record is logged and
// skipped; a fatal stream error stops the run and is reported via stats.err.
func (p *producer) run(ctx context.Context, out chan<- ingest.Business) {
	defer close(out)

	var s stats
	for {
		if err := ctx.Err(); err != nil {
			s.err = err
			break
		}
		raw, err := p.parser.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.err = err
			break
		}
		s.read++
		biz, keep := p.process(raw, &s)
		if !keep {
			continue
		}
		select {
		case <-ctx.Done():
			s.err = ctx.Err()
			p.done <- s
			return
		case out <- biz:
		}
	}
	p.done <- s
}

// wait blocks until run has finished and returns the accumulated stats.
func (p *producer) wait() stats { return <-p.done }

// process applies the pure stages to one raw record and reports whether it
// survives to be loaded, updating the running tally. Drops (bad record, geo,
// empty category) and the Other-bucket are counted here.
func (p *producer) process(raw json.RawMessage, s *stats) (ingest.Business, bool) {
	rb, err := ingest.Sanitize(raw)
	if err != nil {
		p.logger.Warn("skipping unparseable record", slog.String("err", err.Error()))
		s.read-- // never counted as read: it was not a valid business record
		return ingest.Business{}, false
	}

	switch ingest.GeoFilter(rb.Latitude, rb.Longitude, deref(rb.Address)) {
	case ingest.DropNonMiami:
		s.droppedGeo++
		return ingest.Business{}, false
	case ingest.DropNoAddress:
		s.droppedNoAddr++
		return ingest.Business{}, false
	case ingest.Keep:
	}

	tax := ingest.Normalize(rb.Category, deref(rb.Subcategory))
	switch tax.Decision {
	case ingest.TaxonomyDrop:
		s.droppedCatEmpty++
		return ingest.Business{}, false
	case ingest.TaxonomyBucketed:
		s.bucketedOther++
	case ingest.TaxonomyKeep:
	}

	claimed := ingest.Claimed(rb.ID)
	if claimed {
		s.claimedTrue++
	}
	friends := ingest.FriendCount(rb.ID)
	if friends > 0 {
		s.friendNonzero++
	}

	return toBusiness(rb, tax, claimed, friends), true
}

// toBusiness assembles the final row from the sanitized record, the normalized
// taxonomy, and the synthesized claimed flag + friend count.
func toBusiness(rb ingest.RawBusiness, tax ingest.Taxonomy, claimed bool, friends int) ingest.Business {
	return ingest.Business{
		ID:                rb.ID,
		Name:              rb.Name,
		Category:          tax.Category,
		Subcategory:       emptyToNil(tax.Subcategory),
		Specialty:         rb.Specialty,
		Archetype:         tax.Archetype,
		Address:           rb.Address,
		Neighborhood:      rb.Neighborhood,
		Latitude:          rb.Latitude,
		Longitude:         rb.Longitude,
		LemonScore:        rb.LemonScore,
		GoogleRating:      rb.GoogleRating,
		GoogleReviewCount: rb.GoogleReviewCount,
		PriceRange:        rb.PriceRange,
		Hours:             rb.Hours,
		Photos:            rb.Photos,
		About:             rb.About,
		UniversalTags:     rb.UniversalTags,
		SpecificTags:      rb.SpecificTags,
		IsClaimed:         claimed,
		FriendCount:       friends,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// emptyToNil maps the taxonomy's string subcategory to a nullable pointer so an
// absent subcategory is stored as SQL NULL, not "".
func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
