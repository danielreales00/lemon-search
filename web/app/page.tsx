import { SearchPage } from './SearchPage';

export default function HomePage(): React.JSX.Element {
  return (
    <main className="container">
      <h1 className="title">Lemon Search</h1>
      <p className="tagline">Find any service in Miami.</p>
      <SearchPage />
    </main>
  );
}
