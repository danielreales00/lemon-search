import type { Metadata } from 'next';
import type { ReactNode } from 'react';

import './globals.css';

export const metadata: Metadata = {
  title: 'Lemon — Search Miami',
  description:
    'From dinner to dentist. Discover any business in Miami — typo-tolerant, ranked, sub-100ms search over ~23k local businesses.',
};

export default function RootLayout({ children }: { children: ReactNode }): React.JSX.Element {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
