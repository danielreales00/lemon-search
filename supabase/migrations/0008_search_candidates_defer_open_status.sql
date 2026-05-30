-- 0008_search_candidates_defer_open_status.sql
-- Perf: defer the lemon_open_status lateral until AFTER the recall LIMIT.
--
-- 0007 computed lemon_open_status (which parses each business's hours JSON) in the
-- FROM, so it ran once per row matching the recall predicate — e.g. "bar" matches
-- 3,407 rows, so open-status was computed 3,407 times before the top-150 LIMIT
-- (EXPLAIN: ~157ms, dominated by `Function Scan on lemon_open_status loops=3407`).
-- The open-status output is only needed for the rows we return, and the recall
-- ORDER BY never uses it, so this restructures into: (1) a `recalled` CTE that
-- ranks + LIMITs on the cheap signals (text rank, similarity, popularity,
-- distance), then (2) computes lemon_open_status only for the survivors. "bar"
-- drops to ~25ms; the win scales with how many rows a term matches.
--
-- require_open: the open filter needs open-status, which now lives past the LIMIT,
-- so when it's set the recall over-fetches (lim*5) and the outer query filters to
-- open and re-applies LIMIT — still ~5x fewer open-status calls than before, and
-- the candidate pool stays large enough for the 15-result ranker. Unknown-hours
-- rows are kept (soft-open, decision D8).
--
-- RETURNS TABLE column order is UNCHANGED from 0007 (the Go scan depends on it).
-- Forward-only + idempotent: drop the 0007 12-arg signature, recreate.

drop function if exists search_candidates(
  text, float8, float8, timestamptz, int, text, text[], text[], text[], text[], boolean, vector
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
  semantic_ids as (
    select array(
      select b2.id
      from businesses b2
      where p_query_vec is not null
        and b2.embedding is not null
      order by b2.embedding <=> p_query_vec
      limit lim
    ) as ids
  ),
  -- Rank + LIMIT on the cheap signals only (no open-status). The order key is
  -- carried as columns so the outer query re-applies it without recomputing.
  recalled as (
    select
      b.id, b.name, b.category, b.subcategory, b.archetype, b.neighborhood,
      case when b.loc is null then 1e9
           else earth_distance(b.loc, p.user_loc) / 1000.0 end  as distance_km,
      b.lemon_score, b.google_rating,
      coalesce(b.google_review_count, 0)                        as google_review_count,
      b.price_range, b.photo_count,
      case when b.photos is not null and cardinality(b.photos) >= 1
           then b.photos[1] else null end                       as photo_url,
      b.is_claimed, b.friend_count, b.is_new, b.hours,
      (coalesce(ts_rank_cd(b.search_vector, p.tsq), 0)
         + coalesce(similarity(b.name, p.q_clean), 0))          as txt_score,
      case when p.q_clean is null then 0 else 1 end             as has_q
    from businesses b
    cross join params p
    cross join semantic_ids s
    where
      (
        p.q_clean is null
        or b.search_vector @@ p.tsq
        or b.name % p.q_clean
        or b.name ilike p.q_clean || '%'
        or b.id = any(s.ids)
      )
      and (p_category is null or b.category = p_category)
      and (cardinality(p_subcategories) = 0 or b.subcategory = any(p_subcategories))
      and (cardinality(p_universal) = 0 or b.universal_tags && p_universal)
      and (cardinality(p_specific) = 0 or b.specific_tags && p_specific)
      and (cardinality(p_prices) = 0 or b.price_range = any(p_prices))
    order by has_q desc, txt_score desc, google_review_count desc, distance_km asc, id
    -- Over-fetch when filtering by open-status (applied after the lateral below),
    -- so enough survive the filter to fill the ranker's candidate pool.
    limit (case when p_require_open then lim * 5 else lim end)
  )
  select
    r.id, r.name, r.category, r.subcategory, r.archetype, r.neighborhood,
    r.distance_km,
    r.lemon_score::float8, r.google_rating::float8, r.google_review_count,
    r.price_range, r.photo_count, r.photo_url, r.is_claimed, r.friend_count, r.is_new,
    os.is_open_now, coalesce(os.opens_later, false) as opens_later, r.hours
  from recalled r
  -- local_ts is inlined (not referenced from `params`) so `params` stays
  -- single-use and inlinable; referencing it twice would materialize it, and a
  -- materialized tsq can't drive the GIN index — forcing a full table scan.
  cross join lateral lemon_open_status(r.hours, (now_ts at time zone 'America/New_York')::timestamp) os
  where (not p_require_open or os.is_open_now is not false)
  order by r.has_q desc, r.txt_score desc, r.google_review_count desc, r.distance_km asc, r.id
  limit lim;
$fn$;
