'use client';

import type { Business } from '@/lib/api';

interface ResultsListProps {
  results: Business[];
}

function openNowLabel(isOpenNow: boolean | null): string {
  if (isOpenNow === null) {
    return 'hours unknown';
  }
  return isOpenNow ? 'open' : 'closed';
}

function openNowClass(isOpenNow: boolean | null): string {
  if (isOpenNow === null) {
    return 'chip chip--neutral';
  }
  return isOpenNow ? 'chip chip--open' : 'chip chip--closed';
}

function formatDistance(km: number): string {
  if (km < 1) {
    return `${Math.round(km * 1000)}m`;
  }
  return `${km.toFixed(1)}km`;
}

interface BusinessRowProps {
  business: Business;
}

function BusinessRow({ business }: BusinessRowProps): React.JSX.Element {
  const categoryLabel =
    business.subcategory !== null
      ? `${business.category} · ${business.subcategory}`
      : business.category;

  return (
    <li className="result-item">
      {business.photo_url !== null && (
        // The spec requires a plain lazy <img> thumbnail; Next.js image optimisation
        // is intentionally skipped here per the contract requirements. Many source
        // photo URLs are dead (Google-hosted 404 / ORB-blocked), so hide a failed
        // image rather than show a broken-icon (no broken states, fewer console
        // 404s a user would notice). The dead-URL data issue is flagged in the writeup.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={business.photo_url}
          alt=""
          loading="lazy"
          className="result-photo"
          width={64}
          height={64}
          onError={(e) => {
            e.currentTarget.style.display = 'none';
          }}
        />
      )}
      <div className="result-body">
        <div className="result-header">
          <span className="result-name">{business.name}</span>
          <div className="result-chips">
            <span className={openNowClass(business.is_open_now)}>
              {openNowLabel(business.is_open_now)}
            </span>
            {business.is_new && <span className="chip chip--new">New</span>}
            {business.is_claimed && <span className="chip chip--claimed">Claimed</span>}
          </div>
        </div>
        <div className="result-meta">
          <span className="result-category">{categoryLabel}</span>
          {business.neighborhood !== null && (
            <span className="result-neighborhood">{business.neighborhood}</span>
          )}
          <span className="chip chip--distance">{formatDistance(business.distance_km)}</span>
        </div>
        <div className="result-stats">
          {business.rating !== null ? (
            <>
              <span className="result-rating">★ {business.rating.toFixed(1)}</span>
              <span className="result-reviews">({business.review_count.toLocaleString()})</span>
            </>
          ) : (
            <span className="result-rating result-rating--none">no rating yet</span>
          )}
          {business.price_range !== null && (
            <span className="result-price">{business.price_range}</span>
          )}
        </div>
      </div>
    </li>
  );
}

export function ResultsList({ results }: ResultsListProps): React.JSX.Element {
  return (
    <ul className="results-list" aria-label="Search results">
      {results.map((business) => (
        <BusinessRow key={business.id} business={business} />
      ))}
    </ul>
  );
}
