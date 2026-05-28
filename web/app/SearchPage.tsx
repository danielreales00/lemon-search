'use client';

import { useCallback, useRef, useState } from 'react';

import { ResultsList } from '@/components/ResultsList';
import { SearchBar } from '@/components/SearchBar';
import { SearchError, searchBusinesses } from '@/lib/api';

import type { Business } from '@/lib/api';

type SearchState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ok'; results: Business[]; query: string; totalMs: number };

export function SearchPage(): React.JSX.Element {
  const [state, setState] = useState<SearchState>({ status: 'idle' });
  const abortRef = useRef<AbortController | undefined>(undefined);

  const handleSearch = useCallback((query: string): void => {
    if (abortRef.current !== undefined) {
      abortRef.current.abort();
    }

    if (query.trim() === '') {
      setState({ status: 'idle' });
      return;
    }

    const controller = new AbortController();
    abortRef.current = controller;
    setState({ status: 'loading' });

    void searchBusinesses({ q: query }, controller.signal).then(
      (data) => {
        setState({
          status: 'ok',
          results: data.results,
          query: data.query,
          totalMs: data.timings.total_ms,
        });
      },
      (err: unknown) => {
        if (err instanceof Error && err.name === 'AbortError') {
          return;
        }
        const message =
          err instanceof SearchError
            ? `Search failed (${err.status.toString()})`
            : 'Something went wrong. Please try again.';
        setState({ status: 'error', message });
      },
    );
  }, []);

  const handleQueryChange = useCallback((query: string): void => {
    if (query.trim() === '') {
      if (abortRef.current !== undefined) {
        abortRef.current.abort();
      }
      setState({ status: 'idle' });
    }
  }, []);

  return (
    <>
      <SearchBar onResults={handleSearch} onQueryChange={handleQueryChange} />
      <div className="search-results" role="region" aria-label="Search results">
        {state.status === 'idle' && (
          <p className="search-prompt">Search Miami businesses, services, and more.</p>
        )}
        {state.status === 'loading' && (
          <p className="search-loading" aria-live="polite">
            Searching…
          </p>
        )}
        {state.status === 'error' && (
          <p className="search-error" role="alert">
            {state.message}
          </p>
        )}
        {state.status === 'ok' && state.results.length === 0 && (
          <p className="search-empty" aria-live="polite">
            No results for &ldquo;{state.query}&rdquo;.
          </p>
        )}
        {state.status === 'ok' && state.results.length > 0 && (
          <>
            <p className="search-timing" aria-live="polite">
              {state.results.length} result{state.results.length !== 1 ? 's' : ''} in{' '}
              {state.totalMs}ms
            </p>
            <ResultsList results={state.results} />
          </>
        )}
      </div>
    </>
  );
}
