'use client';

interface SearchBarProps {
  value: string;
  onChange: (query: string) => void;
}

export function SearchBar({ value, onChange }: SearchBarProps): React.JSX.Element {
  return (
    <div className="search-bar">
      <svg
        className="search-icon"
        viewBox="0 0 24 24"
        width="20"
        height="20"
        aria-hidden="true"
        focusable="false"
      >
        <path
          d="M11 4a7 7 0 1 0 4.2 12.6l4.1 4.1 1.4-1.4-4.1-4.1A7 7 0 0 0 11 4Zm0 2a5 5 0 1 1 0 10 5 5 0 0 1 0-10Z"
          fill="currentColor"
        />
      </svg>
      <input
        type="search"
        aria-label="Search Miami businesses"
        placeholder="Try “chill place to work” or “tacos near me”"
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        autoComplete="off"
        spellCheck={false}
        autoFocus
        className="search-input"
      />
    </div>
  );
}
