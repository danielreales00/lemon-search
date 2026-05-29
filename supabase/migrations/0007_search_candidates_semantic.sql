-- 0007_search_candidates_semantic.sql
-- Add a semantic recall channel to search_candidates (ADR-0006, E4). A query
-- embedding (p_query_vec) widens recall with the top-`lim` nearest businesses by
-- cosine distance (the pgvector HNSW index on businesses.embedding), UNIONed into
-- the candidate set the 7-signal ranker scores. Relevance stays in RETRIEVAL —
-- this adds candidates, it does not rank them (ADR-0004 / ADR-0006 §4).
--
-- INVARIANT — flag-off parity: p_query_vec NULL (the case whenever
-- LEMON_FF_SEMANTIC is off, and for the bench-runner) makes the semantic_ids
-- subquery short-circuit to the empty set, so `b.id = any('{}')` adds no rows and
-- the statement is byte-identical to 0005. The HNSW scan never runs when NULL.
--
-- Additive, never displacing: the semantic ids only EXTEND the recall predicate
-- (logical OR). The overlay filters, ORDER BY, and LIMIT are unchanged, so the
-- exact-name / typo / prefix lexical matches keep their positions and semantic
-- rows (text_score ≈ 0) fill the remaining pool slots. RRF interleaving across
-- the two arms is a tuning lever for E5 (#93), measured before adopting.
--
-- RETURNS TABLE column order is UNCHANGED from 0005 — the Go scan
-- (candidateScanDests in internal/retrieve/postgres/repo.go) depends on it.
--
-- Forward-only + idempotent (CI applies twice): drop the 0005 (11-arg) signature
-- by argument types, then CREATE OR REPLACE the new 12-arg form. The 12-arg
-- return type does not change between applies, so the recreate is unconditional.

drop function if exists search_candidates(
  text, float8, float8, timestamptz, int, text, text[], text[], text[], text[], boolean
);

create or replace function search_candidates(
  q              text,
  lat            float8,
  lng            float8,
  now_ts         timestamptz,
  lim            int,
  p_category     text    default null,
  p_subcategories text[] default '{}',
  p_universal    text[]  default '{}',
  p_specific     text[]  default '{}',
  p_prices       text[]  default '{}',
  p_require_open boolean default false,
  p_query_vec    vector(384) default null
)
returns table (
  id                  uuid,
  name                text,
  category            text,
  subcategory         text,
  archetype           text,
  neighborhood        text,
  distance_km         float8,
  lemon_score         float8,
  google_rating       float8,
  google_review_count int,
  price_range         text,
  photo_count         int,
  photo_url           text,
  is_claimed          boolean,
  friend_count        int,
  is_new              boolean,
  is_open_now         boolean,
  opens_later         boolean,
  hours               jsonb
)
language sql
stable
as $fn$
  with params as (
    select
      nullif(btrim(coalesce(q, '')), '')                  as q_clean,
      case
        when nullif(btrim(coalesce(q, '')), '') is null then null::tsquery
        else websearch_to_tsquery('english', q)
      end                                                 as tsq,
      ll_to_earth(lat, lng)                               as user_loc,
      (now_ts at time zone 'America/New_York')::timestamp as local_ts
  ),
  -- Top-`lim` nearest by cosine distance (HNSW). Empty — no scan — when no query
  -- vector is supplied, which is what makes p_query_vec NULL a no-op. Overlay
  -- filters are applied by the outer query's AND clauses, not here.
  semantic_ids as (
    select array(
      select b2.id
      from businesses b2
      where p_query_vec is not null
        and b2.embedding is not null
      order by b2.embedding <=> p_query_vec
      limit lim
    ) as ids
  )
  select
    b.id,
    b.name,
    b.category,
    b.subcategory,
    b.archetype,
    b.neighborhood,
    case
      when b.loc is null then 1e9
      else earth_distance(b.loc, p.user_loc) / 1000.0
    end                                                    as distance_km,
    b.lemon_score::float8,
    b.google_rating::float8,
    coalesce(b.google_review_count, 0)                     as google_review_count,
    b.price_range,
    b.photo_count,
    case
      when b.photos is not null and cardinality(b.photos) >= 1 then b.photos[1]
      else null
    end                                                    as photo_url,
    b.is_claimed,
    b.friend_count,
    b.is_new,
    os.is_open_now,
    coalesce(os.opens_later, false)                        as opens_later,
    b.hours
  from businesses b
  cross join params p
  cross join semantic_ids s
  cross join lateral lemon_open_status(b.hours, p.local_ts) os
  where
    (
      p.q_clean is null
      or b.search_vector @@ p.tsq
      or b.name % p.q_clean
      or b.name ilike p.q_clean || '%'
      -- Semantic arm: widen recall to the nearest-neighbour ids (empty when no
      -- query vector, so byte-identical to 0005 in that case).
      or b.id = any(s.ids)
    )
    and (p_category is null or b.category = p_category)
    and (cardinality(p_subcategories) = 0 or b.subcategory = any(p_subcategories))
    and (cardinality(p_universal) = 0 or b.universal_tags && p_universal)
    and (cardinality(p_specific) = 0 or b.specific_tags && p_specific)
    and (cardinality(p_prices) = 0 or b.price_range = any(p_prices))
    -- require_open drops only definitively-closed rows; unknown-hours rows
    -- (is_open_now null) stay as soft-open (CLAUDE.md / decision D8).
    and (not p_require_open or os.is_open_now is not false)
  order by
    -- Recall ordering: blend text rank + name similarity when there's a query;
    -- otherwise surface popular-then-near rows so an empty query is useful.
    -- Semantic-only rows score ≈ 0 here and fill remaining slots after the
    -- lexical matches; the Go 7-signal ranker decides final order.
    case when p.q_clean is null then 0 else 1 end desc,
    (coalesce(ts_rank_cd(b.search_vector, p.tsq), 0)
       + coalesce(similarity(b.name, p.q_clean), 0)) desc,
    coalesce(b.google_review_count, 0) desc,
    case when b.loc is null then 1e9
         else earth_distance(b.loc, p.user_loc) / 1000.0 end asc,
    b.id
  limit lim;
$fn$;
