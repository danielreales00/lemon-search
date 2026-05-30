import { SearchPage } from './SearchPage';

export default function HomePage(): React.JSX.Element {
  return (
    <main className="container">
      <header className="hero">
        <div className="brand">
          <span className="brand-lemon" aria-hidden="true">
            🍋
          </span>
          <span className="brand-name">Lemon</span>
        </div>
        <h1 className="hero-title">From dinner to dentist.</h1>
        <p className="hero-tagline">Discover any business in Miami — one fast search.</p>
      </header>
      <SearchPage />
      <footer className="site-footer">
        Ranked search over ~23k Miami businesses · typo-tolerant · sub-100ms
      </footer>
    </main>
  );
}
