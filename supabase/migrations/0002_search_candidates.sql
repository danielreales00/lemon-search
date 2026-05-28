-- 0002_search_candidates.sql
-- Two-phase retrieval: this is the SQL "phase 1" — recall a candidate set with
-- rich RAW signals in a single round-trip. The pure Go ranker
-- (internal/rank) does precision/scoring; this function must NOT score, and
-- must NOT hard-filter on distance or archetype (those are the ranker's job).
--
-- Signature is LOCKED (docs/roadmap/02-search-core.md): Stage 3 only *fills*
-- the currently-NULL overlay params; it does not change the signature.
--
-- Hours evaluation is split into two immutable helpers (lemon_parse_time,
-- lemon_open_status) so the open-now / opens-later math is testable and the
-- now_ts → Miami-local conversion happens once per call (in a CTE), not per
-- row in a correlated subquery.
--
-- See docs/ranking/semantics.md §7 and docs/data/schema.md (hours JSONB shape).

-- America/New_York is Miami's wall-clock zone. Business hours in the source
-- data are local wall-clock times; now_ts arrives as a timestamptz, so we must
-- convert it to Miami local time before extracting the weekday and time-of-day.
-- Kept as a constant here for clarity / single point of change.

-- ---------------------------------------------------------------------------
-- lemon_parse_time: best-effort parse of one source time token into a `time`.
--
-- The source `hours` JSONB is mostly clean ({"open":"9:00 AM","close":"6:30 PM"})
-- but ~0.6% of day entries use messy formats (lowercase am/pm, "a.m." dots,
-- "12:00 AM (Next day)", 24h "00:00", or compound ranges like "9 AM - 5 PM").
-- We parse the clean 12h/24h single-token forms and return NULL for anything we
-- cannot confidently parse. A NULL propagates to "unknown for that day", which
-- the semantics treat as soft-open (never a hard filter) — the conservative
-- call documented in docs/data/quality.md.
-- ---------------------------------------------------------------------------
create or replace function lemon_parse_time(raw text)
returns time
language plpgsql
immutable
as $fn$
declare
  s    text;
  h    int;
  mi   int;
  ampm text;
  m    text[];
begin
  if raw is null then
    return null;
  end if;

  s := upper(btrim(raw));
  -- "12:00 AM (Next day)" → "12:00 AM"; strip "a.m." dots; fold "A M" → "AM".
  s := regexp_replace(s, '\s*\(NEXT DAY\)\s*$', '');
  s := regexp_replace(s, '[.]', '', 'g');
  s := regexp_replace(s, 'A\s*M', 'AM', 'g');
  s := regexp_replace(s, 'P\s*M', 'PM', 'g');
  s := btrim(s);

  -- 12-hour clock, minutes optional: "9 AM", "9:00 AM", "12:30 PM".
  m := regexp_match(s, '^([0-9]{1,2})(?::([0-9]{2}))?\s*(AM|PM)$');
  if m is not null then
    h    := m[1]::int;
    mi   := coalesce(m[2], '0')::int;
    ampm := m[3];
    if h > 12 or mi > 59 then
      return null;
    end if;
    if ampm = 'AM' then
      if h = 12 then
        h := 0;            -- 12 AM (and the stray "0 AM" in data) = midnight
      end if;
    elsif h <> 12 then
      h := h + 12;         -- 12 PM stays noon; 1..11 PM shift by 12
    end if;
    if h > 23 then
      return null;
    end if;
    return make_time(h, mi, 0);
  end if;

  -- 24-hour clock: "00:00", "13:30".
  m := regexp_match(s, '^([0-9]{1,2}):([0-9]{2})$');
  if m is not null then
    h  := m[1]::int;
    mi := m[2]::int;
    if h > 23 or mi > 59 then
      return null;
    end if;
    return make_time(h, mi, 0);
  end if;

  return null;
end;
$fn$;

-- ---------------------------------------------------------------------------
-- lemon_open_status: evaluate the hours JSONB for a given Miami-local instant.
--
-- local_ts is already in Miami wall-clock (the caller converts via AT TIME
-- ZONE), so weekday and time-of-day come straight off it. Semantics
-- (docs/ranking/semantics.md §7):
--   hours null / day entry unknown  → is_open_now = NULL, opens_later = false
--   now within an open interval      → is_open_now = true
--   closed now but an interval opens later today → false, opens_later = true
--   closed now and nothing later     → false, false
--
-- A day entry shaped {"open","close"} where close <= open is treated as an
-- overnight wrap (e.g. open 5pm, close 12am): open iff now >= open (the segment
-- up to midnight today). Compound/string day entries that don't parse fall
-- through to "unknown" (NULL).
-- ---------------------------------------------------------------------------
create or replace function lemon_open_status(
  hours    jsonb,
  local_ts timestamp,
  out is_open_now boolean,
  out opens_later boolean
)
language plpgsql
immutable
as $fn$
declare
  day_key text;
  entry   jsonb;
  o       time;
  c       time;
  now_t   time;
begin
  is_open_now := null;
  opens_later := false;

  if hours is null or jsonb_typeof(hours) <> 'object' then
    return;
  end if;

  day_key := lower(to_char(local_ts, 'FMday'));  -- 'monday' .. 'sunday'
  entry   := hours -> day_key;

  -- No entry for today, or the entry isn't a {open,close}/{closed} object →
  -- unknown for today.
  if entry is null or jsonb_typeof(entry) <> 'object' then
    return;
  end if;

  -- Explicitly closed all day.
  if coalesce((entry ->> 'closed')::boolean, false) then
    is_open_now := false;
    return;
  end if;

  o := lemon_parse_time(entry ->> 'open');
  c := lemon_parse_time(entry ->> 'close');

  -- Can't parse the interval → unknown (leave is_open_now NULL).
  if o is null or c is null then
    return;
  end if;

  now_t := local_ts::time;

  if c > o then
    -- Normal same-day interval.
    if now_t >= o and now_t < c then
      is_open_now := true;
    else
      is_open_now := false;
      opens_later := now_t < o;  -- reopens later today
    end if;
  else
    -- Overnight wrap (close <= open, e.g. close 12am/2am). Open from `o` to
    -- midnight counts as today; we never project an interval from yesterday.
    if now_t >= o then
      is_open_now := true;
    else
      is_open_now := false;
      -- It opens again at `o` later today.
      opens_later := true;
    end if;
  end if;
end;
$fn$;

-- ---------------------------------------------------------------------------
-- search_candidates: recall phase. Returns up to `lim` candidates with raw
-- signals, ordered by a blend of full-text rank and name-trigram similarity.
-- Overlay params (tag/category/subcategory/price/require_open) are wired as
-- (param IS NULL OR <cond>) so Stage 2 passing NULL/false is a no-op.
-- ---------------------------------------------------------------------------
create or replace function search_candidates(
  q                  text,
  lat                float8,
  lng                float8,
  now_ts             timestamptz,
  lim                int,
  tag_filter         text[]  default null,
  category_filter    text    default null,
  subcategory_filter text[]  default null,
  price_filter       text[]  default null,
  require_open       boolean default false
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
    -- Overlay filters (no-op when NULL/false).
    and (tag_filter is null
         or b.universal_tags && tag_filter
         or b.specific_tags && tag_filter)
    and (category_filter is null or b.category = category_filter)
    and (subcategory_filter is null or b.subcategory = any(subcategory_filter))
    and (price_filter is null or b.price_range = any(price_filter))
    and (not require_open or os.is_open_now is true)
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
