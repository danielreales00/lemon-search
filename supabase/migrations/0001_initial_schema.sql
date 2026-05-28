-- 0001_initial_schema.sql
-- Initial schema for Lemon Search. Indexes are designed for:
--   - pg_trgm fuzzy/typo matching on `name` and `search_vector`
--   - weighted tsvector full-text on name/subcategory/category/specialty/tags/about
--   - tag-array containment for intent-driven filters
--   - earthdistance (GIST) for geo bounding-box + exact distance
--   - btree on category/archetype for cheap equality filters
--
-- See docs/architecture.md and docs/roadmap/02-search-core.md.

create extension if not exists pg_trgm;
create extension if not exists cube;
create extension if not exists earthdistance;  -- requires cube

create table if not exists businesses (
  id                   uuid primary key,
  name                 text not null,
  category             text not null,
  subcategory          text,
  specialty            text,
  archetype            text not null
                         check (archetype in (
                           'low_stakes_fast_nearby',
                           'medium_stakes_occasion',
                           'high_stakes_one_time',
                           'experiential',
                           'recurring_service',
                           'utility_distance_dominant'
                         )),
  address              text,
  neighborhood         text,
  latitude             double precision,
  longitude            double precision,
  -- `loc` is populated at ingest via ll_to_earth(latitude, longitude) (see the
  -- INSERT…SELECT in docs/data/ingestion.md). It is NOT a STORED generated
  -- column: ll_to_earth is not immutable enough for a generation expression on
  -- PG15, but it's fine in an INSERT/UPDATE and the GIST index works on the
  -- stored values.
  loc                  earth,
  lemon_score          real,
  google_rating        real,
  google_review_count  integer,
  price_range          text,
  hours                jsonb,
  photos               text[],
  photo_count          integer generated always as (
                         coalesce(cardinality(photos), 0)
                       ) stored,
  about                text,
  universal_tags       text[],
  specific_tags        text[],
  is_claimed           boolean not null default false,
  friend_count         integer not null default 0,
  is_new               boolean generated always as (
                         coalesce(google_review_count, 0) < 10
                       ) stored,
  search_vector        tsvector generated always as (
                         setweight(to_tsvector('english', coalesce(name, '')), 'A')
                         || setweight(to_tsvector('english', coalesce(subcategory, '')), 'B')
                         || setweight(to_tsvector('english', coalesce(category, '')), 'C')
                         || setweight(to_tsvector('english', coalesce(specialty, '')), 'C')
                         || setweight(to_tsvector('english',
                              array_to_string(coalesce(specific_tags, '{}'), ' ')), 'C')
                         || setweight(to_tsvector('english', coalesce(about, '')), 'D')
                       ) stored,
  created_at           timestamptz not null default now()
);

create index if not exists idx_biz_name_trgm  on businesses using gin (name gin_trgm_ops);
create index if not exists idx_biz_search_vec on businesses using gin (search_vector);
create index if not exists idx_biz_loc        on businesses using gist (loc);
create index if not exists idx_biz_category   on businesses (category);
create index if not exists idx_biz_archetype  on businesses (archetype);
create index if not exists idx_biz_uni_tags   on businesses using gin (universal_tags);
create index if not exists idx_biz_spec_tags  on businesses using gin (specific_tags);

-- Stable hash so synth columns (is_claimed, friend_count) regenerate
-- deterministically if we re-ingest.
create or replace function lemon_seed(u uuid)
returns double precision language sql immutable as $$
  select ('x' || substr(md5(u::text), 1, 8))::bit(32)::int8 / 4294967296.0
$$;

-- Read-only role for graders. Apply password out-of-band via Supabase Studio.
do $$
begin
  if not exists (select 1 from pg_roles where rolname = 'lemon_grader') then
    create role lemon_grader login;
  end if;
end $$;

grant connect on database postgres to lemon_grader;
grant usage on schema public to lemon_grader;
grant select on all tables in schema public to lemon_grader;
alter default privileges in schema public grant select on tables to lemon_grader;
