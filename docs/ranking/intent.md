# Intent extraction — the lexicon

`intent.Extract(query) → Overlay` runs upstream of retrieval. It maps head
terms in the query to a filter/boost overlay that the SQL `WHERE` clause
ANDs in. Sub-millisecond. No LLM. No embeddings.

The spec wording: *"lightweight intent understanding beyond strict keyword
matching."* This doc is the spec for what we extract.

## What it does *not* do

- It does **not** override archetype assignment. Spec: "every category maps
  to one of six archetypes" — that mapping stays per-business and applies
  to candidates regardless of query. (See ranking decision D6.)
- It does **not** rewrite the query for the trigram/tsvector path. The
  user's original query still drives text relevance; intent narrows the
  candidate set.

## Output shape (C5 contract)

```go
type Overlay struct {
    CategoryFilter      *string     // e.g. "Food & Drinks"
    SubcategoryFilter   []string    // e.g. ["Photography & Video", "Weddings"]
    UniversalTagFilter  []string    // ANDed via array-overlap (&&)
    SpecificTagFilter   []string
    PriceFilter         []string    // ["$", "$$"]
    RequireOpenNow      bool
}
```

The type lives in `domain` (`domain.Overlay`), not `intent` — see contract C5.
The Postgres adapter consumes it and adds equivalent clauses to the retrieval
SQL. Empty fields are no-ops.

## Tokenization

```
raw query → lowercase → unaccent → split on whitespace + punctuation
                       │
                       └── preserve original for trigram fallback
```

- **Lowercase**: ASCII case-fold.
- **Unaccent**: `café` → `cafe`, `niño` → `nino`. NFD normalize + strip
  combining marks (Go: `golang.org/x/text/unicode/norm` +
  `transform.RemoveFunc`).
- **Split**: `regexp("[^a-z0-9']+")` (apostrophe preserved for `i'm`).

After tokenization we check **unigrams** and **bigrams** against the
lexicon. Trigrams are not used.

## The lexicon

Each entry maps a *head term* (unigram or bigram, post-normalization) to
overlay contributions. Multiple matching entries are merged additively
(filters union; `RequireOpenNow` is logical-OR).

### Price family

| Token | Adds |
|---|---|
| `cheap` | `PriceFilter += ["$", "$$"]` |
| `affordable` | `PriceFilter += ["$", "$$"]` |
| `budget` | `PriceFilter += ["$", "$$"]` |
| `fancy` | `PriceFilter += ["$$$", "$$$$"]`, `UniversalTagFilter += ["upscale"]` |
| `upscale` | `PriceFilter += ["$$$", "$$$$"]`, `UniversalTagFilter += ["upscale"]` |
| `nice` | `UniversalTagFilter += ["upscale"]` |

### Time family

| Token | Adds |
|---|---|
| `open now` | `RequireOpenNow = true` |
| `tonight` | `RequireOpenNow = true` |
| `late night` | `UniversalTagFilter += ["late-night"]`, `SpecificTagFilter += ["late-night-food"]` |
| `happy hour` | `SpecificTagFilter += ["happy-hour"]` |
| `brunch` | `SpecificTagFilter += ["brunch"]` |
| `breakfast` | `SpecificTagFilter += ["breakfast"]` |
| `lunch` | `SpecificTagFilter += ["lunch"]` |
| `dinner` | `SpecificTagFilter += ["dinner"]` |

### Audience family

| Token | Adds |
|---|---|
| `date night` | `UniversalTagFilter += ["date-night"]` |
| `kid friendly` | `UniversalTagFilter += ["kid-friendly", "family-friendly"]` |
| `family` | `UniversalTagFilter += ["family-friendly"]` |
| `solo` | `UniversalTagFilter += ["solo-friendly"]` |
| `group` | `UniversalTagFilter += ["group-friendly"]` |
| `tourist` | `UniversalTagFilter += ["tourist-friendly"]` |

### Setting family

| Token | Adds |
|---|---|
| `outdoor` | `UniversalTagFilter += ["outdoor-seating"]` |
| `rooftop` | `SpecificTagFilter += ["rooftop"]` |
| `cozy` | `UniversalTagFilter += ["cozy"]` |
| `quiet` | `UniversalTagFilter += ["quiet"]` |
| `lively` | `UniversalTagFilter += ["lively"]` |
| `instagrammable` | `UniversalTagFilter += ["instagrammable"]` |

### Domain pulls (category / subcategory narrowing)

These narrow the candidate set to a specific subcategory when the query
contains the domain word.

| Token | Adds |
|---|---|
| `wedding` | `CategoryFilter = "Events"`, `SubcategoryFilter += ["Weddings", "Photography & Video", "DJ / Music", "Florist", "Catering"]` |
| `photographer` | `CategoryFilter = "Events"`, `SubcategoryFilter += ["Photography & Video"]` |
| `emergency` | `RequireOpenNow = true` (and the next token usually narrows: `tow` → Towing & Roadside) |
| `tow` | `SubcategoryFilter += ["Towing & Roadside"]` |
| `personal trainer` | `SubcategoryFilter += ["Personal Training"]` |
| `dog walker` | `SubcategoryFilter += ["Walking"]` |
| `cleaner` | `SubcategoryFilter += ["Cleaning"]` |
| `dentist` | (out of taxonomy — no-op) |

### Food domain (direct specific_tag match)

These are common food queries where the user types the food name; we
narrow by `SpecificTagFilter` rather than rely only on trigram.

| Token | Adds |
|---|---|
| `sushi` | `SpecificTagFilter += ["sushi"]`, `CategoryFilter = "Food & Drinks"` |
| `tacos` | `SpecificTagFilter += ["tacos"]`, `CategoryFilter = "Food & Drinks"` |
| `coffee` | `SpecificTagFilter += ["coffee"]`, `CategoryFilter = "Food & Drinks"` |
| `pizza` | `SpecificTagFilter += ["pizza"]`, `CategoryFilter = "Food & Drinks"` |
| `burger` | `SpecificTagFilter += ["burgers"]`, `CategoryFilter = "Food & Drinks"` |
| `seafood` | `SpecificTagFilter += ["seafood"]`, `CategoryFilter = "Food & Drinks"` |
| `vegan` | `SpecificTagFilter += ["vegetarian", "vegan"]`, `CategoryFilter = "Food & Drinks"` |
| `cocktails` | `SpecificTagFilter += ["cocktails"]`, `CategoryFilter = "Food & Drinks"` |
| `wine` | `SpecificTagFilter += ["wine"]`, `CategoryFilter = "Food & Drinks"` |
| `beer` | `SpecificTagFilter += ["beer"]`, `CategoryFilter = "Food & Drinks"` |

(The lexicon is a Go table. New entries are a one-line addition.)

### Idiomatic "I'm hungry"

| Token | Adds |
|---|---|
| `hungry` | `CategoryFilter = "Food & Drinks"`, `RequireOpenNow = true` |
| `i'm hungry` (bigram) | same as `hungry` |

## Precedence + merging

The extractor walks every unigram and every consecutive bigram. Each
matching entry contributes additively:

- `*string` fields (`CategoryFilter`): **last write wins** if multiple
  entries set them. Most narrow tokens set the same value, so collisions
  are rare. If e.g. `wedding` (Events) and `coffee` (Food & Drinks) both
  match — unusual — the last one wins; the trigram score on the broader
  candidate set still surfaces the more relevant matches.
- `[]string` fields: union (dedup).
- `bool` fields: logical OR.

We do **not** do "exclusive" intent. The query `cheap kid friendly tacos`
unions all three contributions: `PriceFilter` low, `UniversalTagFilter`
kid-friendly + family-friendly, `SpecificTagFilter` tacos. Narrowing is
intersective via the AND-joined SQL clauses, which is the right behavior.

## Fallback (no-match)

If zero lexicon entries match the query, `Overlay` is the zero value. The
retrieval phase runs trigram + tsvector + tag-array overlap (`&&` on
universal/specific tags from the query bigrams) without additional
filters. The user gets a broad search.

## What this lexicon catches (and what it doesn't)

| Query | Caught | Notes |
|---|---|---|
| `cheap restaurants` | yes | price + category |
| `i'm hungry` | yes | category + open-now |
| `date night spot` | yes | universal_tags `date-night` |
| `wedding photographer` | yes | events / photography |
| `emergency tow` | yes | open-now + subcategory |
| `kid friendly tacos` | yes | tag + tag + specific_tag |
| `a place to study quietly` | partial | `quiet` matches; "study" doesn't (no tag) |
| `something fun for our anniversary` | no | "fun" + "anniversary" not in lexicon |
| `i need a haircut by friday` | partial | "haircut" (specific_tag `hair-cuts`) — no scheduling |

V2 improvement: an embedding-based fallback for queries with no lexicon
hits. Mentioned in writeup.

## Implementation notes

- The lexicon is a static `map[string]rule` in
  `api/internal/intent/lexicon.go`, frozen at startup.
- The extractor is pure; given the same query string it returns the same
  `Overlay`. Easy to unit-test (table-driven).
- Performance budget: < 1 ms over a query of ≤ 200 chars.

## Cross-references

- Output struct (C5 contract): [../roadmap/05-architectural-contracts.md](../roadmap/05-architectural-contracts.md)
- How retrieval consumes the overlay: `api/internal/retrieve/postgres/query.go` (lands at Stage 3)
- Ranking semantics (consumed *after* retrieval): [semantics.md](semantics.md)
