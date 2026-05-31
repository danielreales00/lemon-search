// Compile-time guard: a sample C4 payload must satisfy SearchResponse with no `any` widening.
// No test runner is configured (package.json has no "test" script), so this file is the
// contract check — `tsc --noEmit` will fail if the shape drifts from the wire type.
// Configured as a knip entry so it is not flagged as an unused file.
import type { SearchResponse } from './api';

// tsc will error here if the literal no longer matches the SearchResponse shape.
export const contractSample = {
  query: 'joes barbr near me',
  results: [
    {
      id: 'uuid',
      name: "Joe's Barber Shop",
      category: 'Beauty',
      subcategory: 'Barbershop',
      archetype: 'low_stakes_fast_nearby',
      neighborhood: 'Brickell',
      distance_km: 1.2,
      rating: 4.7,
      review_count: 812,
      price_range: '$$',
      photo_url: 'https://example.com/photo.jpg',
      is_claimed: true,
      friend_count: 2,
      is_new: false,
      is_open_now: true,
      score: 0.81,
    },
  ],
  timings: {
    intent_ms: 0,
    embed_ms: 0,
    sql_ms: 18,
    rerank_ms: 3,
    total_ms: 27,
  },
} satisfies SearchResponse;
