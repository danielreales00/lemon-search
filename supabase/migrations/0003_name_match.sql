-- 0003_name_match.sql
-- Coverage- and per-word-edit-distance matching for the exact-name pin.
--
-- The 0.85 whole-string trigram threshold conflated two things: typos (which
-- should be forgiven) and coverage (how much of the name the query spans). A
-- typo'd full name and a short category prefix land in the same similarity band
-- (~0.5-0.6), so no threshold separates them. This decouples them:
--   * typos      -> per-word levenshtein within the spec's 1-4 char tolerance
--   * coverage   -> the query must span (most of) the business name
-- See docs/ranking/semantics.md "Exact-name pin".

create extension if not exists fuzzystrmatch;  -- provides levenshtein()

-- lemon_name_match(q, nm) returns name-token coverage in (0,1], or 0 when the
-- query is not a plausible full-name match. It is 0 unless (a) the query has
-- enough tokens to span the name and (b) every query token matches some name
-- token within per-word edit tolerance; otherwise it returns the fraction of
-- name tokens matched by a query token. Immutable so it is safe in WHERE/ORDER.
create or replace function lemon_name_match(q text, nm text)
returns double precision language plpgsql immutable as $$
declare
  qt        text[] := regexp_split_to_array(btrim(lower(q)),  '\s+');
  nt        text[] := regexp_split_to_array(btrim(lower(nm)), '\s+');
  nq        int;
  nn        int;
  qtok      text;
  ntok      text;
  best      int;
  tol       int;
  matched_n int := 0;
  matched_q int := 0;
begin
  nq := coalesce(array_length(qt, 1), 0);
  nn := coalesce(array_length(nt, 1), 0);
  if nq = 0 or nn = 0 then
    return 0;
  end if;
  -- The query must be long enough (in tokens) to plausibly be the whole name;
  -- this is what rejects "taco" -> "Taco Taco" (1 token cannot span 2).
  if nq < ceil(0.8 * nn) then
    return 0;
  end if;

  -- Each name token: matched if some query token is within per-word tolerance.
  foreach ntok in array nt loop
    best := 1000;
    foreach qtok in array qt loop
      best := least(best, levenshtein(ntok, qtok));
    end loop;
    tol := greatest(1, least(4, ceil(char_length(ntok) / 4.0)::int));
    if best <= tol then
      matched_n := matched_n + 1;
    end if;
  end loop;

  -- Each query token must match some name token (rejects a garbage extra word).
  foreach qtok in array qt loop
    best := 1000;
    foreach ntok in array nt loop
      best := least(best, levenshtein(qtok, ntok));
    end loop;
    tol := greatest(1, least(4, ceil(char_length(qtok) / 4.0)::int));
    if best <= tol then
      matched_q := matched_q + 1;
    end if;
  end loop;

  if matched_q < nq then
    return 0;
  end if;
  return matched_n::double precision / nn;
end $$;
