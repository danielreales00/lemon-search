package config

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/danielreales00/lemon-search/api/internal/domain"
	"gopkg.in/yaml.v3"
)

// ErrInvalidConfig is the sentinel wrapped by every validation failure, so
// callers can branch with errors.Is without matching on message text.
var ErrInvalidConfig = errors.New("invalid ranking config")

// Valid open_status behaviors for an archetype.
const (
	openStatusHardFilter = "hard_filter"
	openStatusSoft       = "soft"
	openStatusIgnore     = "ignore"
)

// Valid signal-formula modes.
const (
	ratingLiteral   = "literal"
	ratingBayesian  = "bayesian"
	distanceLiteral = "literal"
	distanceDecay   = "decay"
)

// Valid bayesian_rating.source values (the rating column the smoothing reads).
const (
	bayesianSourceGoogle = "google_rating"
	bayesianSourceLemon  = "lemon_score"
)

// Load decodes ranking config from r and validates it fail-fast. A successful
// return means the config is structurally complete and every enum-like field
// holds a known value; it does not guarantee weights sum to anything.
func Load(r io.Reader) (*Ranking, error) {
	var cfg Ranking
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decoding ranking config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadFile opens path and delegates to Load. It is a thin wrapper so tests can
// stay path-independent by calling Load with a strings.Reader.
func LoadFile(path string) (*Ranking, error) {
	f, err := os.Open(path) //nolint:gosec // path is operator-supplied config, not user input
	if err != nil {
		return nil, fmt.Errorf("opening ranking config %q: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only handle; close error is irrelevant

	return Load(f)
}

func (r *Ranking) validate() error {
	if err := r.validateRequiredBlocks(); err != nil {
		return err
	}
	if err := r.validateFormulaModes(); err != nil {
		return err
	}
	return r.validateArchetypes()
}

// validateRequiredBlocks rejects a config missing a top-level block the ranker
// relies on. Empty slices/maps are treated as absent.
func (r *Ranking) validateRequiredBlocks() error {
	if len(r.Signals) == 0 {
		return fmt.Errorf("%w: signals list is required", ErrInvalidConfig)
	}
	if len(r.Archetypes) == 0 {
		return fmt.Errorf("%w: archetypes block is required", ErrInvalidConfig)
	}
	if r.SignalFormulas.Rating == "" {
		return fmt.Errorf("%w: signal_formulas.rating is required", ErrInvalidConfig)
	}
	if r.SignalFormulas.Distance == "" {
		return fmt.Errorf("%w: signal_formulas.distance is required", ErrInvalidConfig)
	}
	return nil
}

func (r *Ranking) validateFormulaModes() error {
	switch r.SignalFormulas.Rating {
	case ratingLiteral:
	case ratingBayesian:
		if err := validateBayesianSource(r.SignalFormulas.BayesianRating.Source); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: signal_formulas.rating %q must be one of [%s %s]",
			ErrInvalidConfig, r.SignalFormulas.Rating, ratingLiteral, ratingBayesian)
	}
	switch r.SignalFormulas.Distance {
	case distanceLiteral, distanceDecay:
	default:
		return fmt.Errorf("%w: signal_formulas.distance %q must be one of [%s %s]",
			ErrInvalidConfig, r.SignalFormulas.Distance, distanceLiteral, distanceDecay)
	}
	return nil
}

// validateBayesianSource rejects an unknown rating source. Checked only when
// the bayesian formula is active, so the literal default never trips on it.
func validateBayesianSource(source string) error {
	switch source {
	case bayesianSourceGoogle, bayesianSourceLemon:
		return nil
	default:
		return fmt.Errorf("%w: signal_formulas.bayesian_rating.source %q must be one of [%s %s]",
			ErrInvalidConfig, source, bayesianSourceGoogle, bayesianSourceLemon)
	}
}

// validateArchetypes checks every archetype key is one of the six domain
// constants and its open_status behavior is known. Missing weight keys are not
// an error (they default to 0 per contract C3).
func (r *Ranking) validateArchetypes() error {
	for name, a := range r.Archetypes {
		if !validArchetype(name) {
			return fmt.Errorf("%w: unknown archetype %q", ErrInvalidConfig, name)
		}
		if err := validateOpenStatus(name, a.OpenStatus); err != nil {
			return err
		}
	}
	return nil
}

func validateOpenStatus(name domain.Archetype, status string) error {
	switch status {
	case openStatusHardFilter, openStatusSoft, openStatusIgnore:
		return nil
	default:
		return fmt.Errorf("%w: archetype %q open_status %q must be one of [%s %s %s]",
			ErrInvalidConfig, name, status,
			openStatusHardFilter, openStatusSoft, openStatusIgnore)
	}
}

// validArchetype reports whether name is one of the six canonical archetypes.
// domain owns the constants, so this stays in sync without re-listing them.
func validArchetype(name domain.Archetype) bool {
	switch name {
	case domain.ArchetypeLowStakesFastNearby,
		domain.ArchetypeMediumStakesOccasion,
		domain.ArchetypeHighStakesOneTime,
		domain.ArchetypeExperiential,
		domain.ArchetypeRecurringService,
		domain.ArchetypeUtilityDistanceDominant:
		return true
	default:
		return false
	}
}
