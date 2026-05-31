'use client';

import { useEffect, useState } from 'react';

import { CategoryChips } from '@/components/CategoryChips';
import { ResultsList } from '@/components/ResultsList';
import { SearchBar } from '@/components/SearchBar';
import { SearchError, searchBusinesses } from '@/lib/api';

import type { Business } from '@/lib/api';

type SearchState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ok'; results: Business[]; query: string; totalMs: number };

const DEBOUNCE_MS = 50;

export function SearchPage(): React.JSX.Element {
  const [query, setQuery] = useState('');
  const [state, setState] = useState<SearchState>({ status: 'idle' });

  // One effect owns the search lifecycle: it debounces keystrokes, aborts the
  // in-flight request on the next change (cleanup), and shows loading eagerly so
  // the UI feels instant. Category cards just call setQuery — same path.
  useEffect(() => {
    const q = query.trim();
    // A 1-char query matches ~thousands of names by prefix — useless results and
    // a needlessly expensive recall. Wait for the 2nd char (standard for search-
    // as-you-type); show the category suggestions until then.
    if (q.length < 2) {
      setState({ status: 'idle' });
      return;
    }

    const controller = new AbortController();
    setState({ status: 'loading' });
    const timer = setTimeout(() => {
      void searchBusinesses({ q }, controller.signal).then(
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
    }, DEBOUNCE_MS);

    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [query]);

  return (
    <>
      <SearchBar value={query} onChange={setQuery} />
      <div className="search-results" role="region" aria-label="Search results">
        {state.status === 'idle' && <CategoryChips onPick={setQuery} />}
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
              <span className="result-count">
                {state.results.length} result{state.results.length !== 1 ? 's' : ''}
              </span>
              <span className="timing-pill">{state.totalMs}ms</span>
            </p>
            <ResultsList results={state.results} />
          </>
        )}
      </div>
    </>
  );
}
