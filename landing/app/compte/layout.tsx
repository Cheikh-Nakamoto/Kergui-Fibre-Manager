import type { Metadata } from 'next';
import './compte.css';

// The customer area (issue #63) is never indexed, and never prerendered:
// every page reads the session and the hub at request time.
export const metadata: Metadata = {
  title: { default: 'Mon compte', template: '%s · Mon compte Kergui' },
  robots: { index: false, follow: false },
};

export const dynamic = 'force-dynamic';

export default function CompteRoot({ children }: { children: React.ReactNode }) {
  return <div className="cl">{children}</div>;
}
