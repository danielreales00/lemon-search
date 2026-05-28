package domain

import "context"

// BusinessRepo is the retrieval port: it returns rich raw candidates and does
// no scoring or archetype filtering (that is the ranker's job). Implemented by
// internal/retrieve/postgres. See contract C1
// (docs/roadmap/05-architectural-contracts.md).
type BusinessRepo interface {
	// Search returns up to opts.Limit candidates ordered by raw text score.
	Search(ctx context.Context, q string, opts SearchOpts) ([]Candidate, error)

	// ExactName returns at most one candidate whose name matches q at or above
	// the similarity threshold (Stage 2 uses 0.85). found=false means no pin —
	// not an error.
	ExactName(ctx context.Context, q string) (c Candidate, found bool, err error)
}
