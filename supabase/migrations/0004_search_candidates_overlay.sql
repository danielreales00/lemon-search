-- 0004_search_candidates_overlay.sql
-- Thread the intent Overlay (contract C5) into retrieval as WHERE clauses so it
-- narrows the candidate set. 0002 wired a single combined tag_filter; the
-- Overlay carries universal and specific tags as distinct fields, so this
-- supersedes search_candidates with one parameter per Overlay field:
--   p_category, p_subcategories, p_universal, p_specific, p_prices, p_require_open.
--
-- INVARIANT: a zero Overlay (every param empty/false — the case whenever
-- LEMON_FF_INTENT is off and for the bench-runner, which passes no overlay) is a
-- no-op. Each clause is `cardinality(...) = 0 or …` (or `… is null`, `not …`),
-- so an empty param widens to TRUE and the result is byte-identical to 0002.
--
-- RETURNS TABLE column order is UNCHANGED from 0002 — the Go scan in
-- internal/retrieve/postgres/repo.go (candidateScanDests) depends on it.
--
-- Forward-only + idempotent (CI applies twice): drop the exact 0002 signature
-- by argument types, then CREATE OR REPLACE the new one. The unrelated helpers
-- lemon_parse_time / lemon_open_status from 0002 are left untouched.

drop function if exists search_candidates(
  text, float8, float8, timestamptz, int, text[], text, text[], text[], boolean
);
-- Also drop this migration's OWN (11-arg) signature before recreating. A later
-- migration (0005) changes the return type, and `create or replace` cannot
-- change a return type — so on a second full apply (CI idempotency check) this
-- create would fail against 0005's already-trimmed function. Dropping first
-- makes the recreate unconditional. (Editing a merged migration is acceptable
-- here: nothing is deployed, so no durable DB has run the old form.)
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
  p_require_open boolean default false
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
  hours               jsonb,
  text_score          float8,
  name_trigram        float8
)
language sql
stable
as $fn$
  with params as (
    select
      nullif(btrim(coalesce(q, '')), '')                  as q_clean,
      -- websearch_to_tsquery on an empty string logs a NOTICE; build the
      -- tsquery only when there's a real query.
      case
        when nullif(btrim(coalesce(q, '')), '') is null then null::tsquery
        else websearch_to_tsquery('english', q)
      end                                                 as tsq,
      ll_to_earth(lat, lng)                               as user_loc,
      (now_ts at time zone 'America/New_York')::timestamp as local_ts
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
    b.hours,
    coalesce(ts_rank_cd(b.search_vector, p.tsq), 0)::float8 as text_score,
    coalesce(similarity(b.name, p.q_clean), 0)::float8      as name_trigram
  from businesses b
  cross join params p
  cross join lateral lemon_open_status(b.hours, p.local_ts) os
  where
    -- Recall: empty query matches everything (fall back to popularity/distance
    -- ordering below); otherwise full-text OR fuzzy/prefix name match.
    (
      p.q_clean is null
      or b.search_vector @@ p.tsq
      or b.name % p.q_clean
      or b.name ilike p.q_clean || '%'
    )
    -- Overlay filters (no-op when the param is empty/null/false).
    and (p_category is null or b.category = p_category)
    and (cardinality(p_subcategories) = 0 or b.subcategory = any(p_subcategories))
    and (cardinality(p_universal) = 0 or b.universal_tags && p_universal)
    and (cardinality(p_specific) = 0 or b.specific_tags && p_specific)
    and (cardinality(p_prices) = 0 or b.price_range = any(p_prices))
    -- require_open drops only definitively-closed rows. Unknown-hours rows
    -- (is_open_now null, ~19% of data) are NEVER hard-filtered — they stay as
    -- soft-open and the ranker scores them 0.7 (see CLAUDE.md / decision D8).
    and (not p_require_open or os.is_open_now is not false)
  order by
    -- Recall ordering: blend text rank + name similarity when there's a query;
    -- otherwise surface popular-then-near rows so an empty query is useful.
    case when p.q_clean is null then 0 else 1 end desc,
    (coalesce(ts_rank_cd(b.search_vector, p.tsq), 0)
       + coalesce(similarity(b.name, p.q_clean), 0)) desc,
    coalesce(b.google_review_count, 0) desc,
    case when b.loc is null then 1e9
         else earth_distance(b.loc, p.user_loc) / 1000.0 end asc,
    b.id
  limit lim;
$fn$;
