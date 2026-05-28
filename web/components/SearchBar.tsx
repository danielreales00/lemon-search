'use client';

import { useEffect, useRef, useState } from 'react';

interface SearchBarProps {
  onResults: (query: string) => void;
  onQueryChange: (query: string) => void;
}

const DEBOUNCE_MS = 50;

export function SearchBar({ onResults, onQueryChange }: SearchBarProps): React.JSX.Element {
  const [value, setValue] = useState('');
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    return () => {
      if (timerRef.current !== undefined) {
        clearTimeout(timerRef.current);
      }
    };
  }, []);

  function handleChange(e: React.ChangeEvent<HTMLInputElement>): void {
    const q = e.target.value;
    setValue(q);
    onQueryChange(q);

    if (timerRef.current !== undefined) {
      clearTimeout(timerRef.current);
    }

    timerRef.current = setTimeout(() => {
      onResults(q);
    }, DEBOUNCE_MS);
  }

  return (
    <div className="search-bar">
      <input
        type="search"
        aria-label="Search Miami businesses"
        placeholder="Search for a business, service, or category…"
        value={value}
        onChange={handleChange}
        autoComplete="off"
        spellCheck={false}
        className="search-input"
      />
    </div>
  );
}
