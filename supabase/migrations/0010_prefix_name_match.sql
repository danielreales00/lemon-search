-- 0010_prefix_name_match.sql
-- Recall fix for PARTIAL-NAME queries (a prefix/fragment of a real business
-- name, e.g. "best florida pest" -> "Best Florida Pest Control").
--
-- Root cause: the pipeline retrieves a text-relevant candidate POOL but the
-- pure ranker scores only the 7 spec signals (distance/rating/popularity/...);
-- text relevance is NOT a ranking signal. So a prefix query that uniquely names
-- a business still loses the top-3 to a more popular / closer unrelated business
-- that happens to share a token. The exact-name pin (lemon_name_match) is the
-- existing "find THE business regardless of signals" override, but it requires
-- the query to SPAN the whole name (nq >= 0.8*nn), so a strict prefix scores 0
-- and never pins. This adds the missing prefix path.
--
-- lemon_prefix_match(q, nm) returns name coverage in (0,1] when q is an in-order,
-- typo-tolerant PREFIX of nm (every query token matches the name token at the
-- same position within per-word Levenshtein tolerance), else 0. It requires
-- >= 2 query tokens, so a bare category word ("coffee", "sushi") never matches
-- (that, plus the cardinality + categorical pin back-offs in the app layer,
-- keeps over-fire at 0). Immutable so it is safe in WHERE/ORDER.
--
-- The spec contract is untouched: ranking is still the 7-signal linear sum. This
-- only widens the precision-tuned exact-name pin (already a max-cost override) to
-- recognize a confident name PREFIX in addition to a typo'd full name.
--
-- Forward-only + idempotent (create or replace).

create or replace function lemon_prefix_match(q text, nm text)
returns double precision language plpgsql immutable as $$
declare
  qt   text[] := regexp_split_to_array(btrim(lower(q)),  '\s+');
  nt   text[] := regexp_split_to_array(btrim(lower(nm)), '\s+');
  nq   int;
  nn   int;
  tol  int;
  i    int;
begin
  nq := coalesce(array_length(qt, 1), 0);
  nn := coalesce(array_length(nt, 1), 0);
  -- Need >= 2 query tokens (a single token is a category word, not a name
  -- prefix) and the name must have at least as many tokens (a real prefix).
  if nq < 2 or nn < nq then
    return 0;
  end if;
  -- Every query token must match the name token at the SAME position within
  -- per-word edit tolerance: an in-order prefix, not a bag of words. This is
  -- what keeps "florida pest" from matching "South Florida Pet Sitter".
  for i in 1 .. nq loop
    tol := greatest(1, least(4, ceil(char_length(qt[i]) / 4.0)::int));
    if levenshtein(qt[i], nt[i]) > tol then
      return 0;
    end if;
  end loop;
  -- Coverage = how much of the name the prefix spans. A longer overlap (more of
  -- the name pinned down) wins when several names share a leading prefix.
  return nq::double precision / nn;
end $$;
