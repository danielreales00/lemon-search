const apiBaseUrl: string = process.env['NEXT_PUBLIC_API_BASE_URL'] ?? 'http://localhost:8080';

// Wire types mirror the C4 contract exactly (snake_case from the API).
export interface Business {
  id: string;
  name: string;
  category: string;
  subcategory: string | null;
  archetype: string;
  neighborhood: string | null;
  distance_km: number;
  rating: number | null; // API maps it from nullable google_rating
  review_count: number;
  price_range: string | null;
  photo_url: string | null;
  is_claimed: boolean;
  friend_count: number;
  is_new: boolean;
  is_open_now: boolean | null;
  score: number;
}

interface SearchTimings {
  intent_ms: number;
  embed_ms: number;
  sql_ms: number;
  rerank_ms: number;
  total_ms: number;
}

export interface SearchResponse {
  query: string;
  results: Business[];
  timings: SearchTimings;
}

interface SearchParams {
  q: string;
  lat?: number;
  lng?: number;
  now?: string;
}

export class SearchError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'SearchError';
  }
}

export async function searchBusinesses(
  params: SearchParams,
  signal: AbortSignal,
): Promise<SearchResponse> {
  const url = new URL(`${apiBaseUrl}/search`);
  url.searchParams.set('q', params.q);
  if (params.lat !== undefined) {
    url.searchParams.set('lat', String(params.lat));
  }
  if (params.lng !== undefined) {
    url.searchParams.set('lng', String(params.lng));
  }
  if (params.now !== undefined) {
    url.searchParams.set('now', params.now);
  }

  const res = await fetch(url.toString(), { signal });

  if (!res.ok) {
    throw new SearchError(res.status, `Search failed: ${res.status} ${res.statusText}`);
  }

  return res.json() as Promise<SearchResponse>;
}
