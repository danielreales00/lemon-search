'use client';

interface CategoryChipsProps {
  onPick: (query: string) => void;
}

const CATEGORIES = [
  { emoji: '☕', label: 'Coffee', q: 'coffee' },
  { emoji: '🍣', label: 'Sushi', q: 'sushi' },
  { emoji: '🍸', label: 'Bars', q: 'bar' },
  { emoji: '🏋️', label: 'Gyms', q: 'gym' },
  { emoji: '🌮', label: 'Tacos', q: 'tacos' },
  { emoji: '💈', label: 'Barbers', q: 'barber' },
  { emoji: '💅', label: 'Nails', q: 'nail salon' },
  { emoji: '🧘', label: 'Yoga', q: 'yoga' },
] as const;

export function CategoryChips({ onPick }: CategoryChipsProps): React.JSX.Element {
  return (
    <div className="categories">
      <p className="categories-label">Popular in Miami</p>
      <div className="category-grid">
        {CATEGORIES.map((c) => (
          <button
            key={c.q}
            type="button"
            className="category-card"
            onClick={() => {
              onPick(c.q);
            }}
          >
            <span className="category-emoji" aria-hidden="true">
              {c.emoji}
            </span>
            <span className="category-label">{c.label}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
