// Thin typed client for the Lemon Search Go API.
// Concrete fetch implementation lands in Stage 2 (docs/roadmap/02-search-core.md).

export type Archetype =
  | 'low_stakes_fast_nearby'
  | 'medium_stakes_occasion'
  | 'high_stakes_one_time'
  | 'experiential'
  | 'recurring_service'
  | 'utility_distance_dominant';

export interface SearchResult {
  id: string;
  name: string;
  category: string;
  subcategory: string | null;
  archetype: Archetype;
  neighborhood: string | null;
  distanceKm: number | null;
  rating: number | null;
  reviewCount: number;
  priceRange: '$' | '$$' | '$$$' | '$$$$' | null;
  photoUrl: string | null;
  isClaimed: boolean;
  isNew: boolean;
  isOpenNow: boolean | null;
  score: number;
}

export interface SearchResponse {
  query: string;
  results: SearchResult[];
  timings: {
    intentMs: number;
    sqlMs: number;
    rerankMs: number;
    totalMs: number;
  };
}
