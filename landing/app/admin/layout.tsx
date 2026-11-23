import type { Metadata } from 'next';
import './admin.css';

// The back-office is never indexed, and never prerendered: every page reads
// the session and the hub at request time.
export const metadata: Metadata = {
  title: { default: 'Back-office', template: '%s · Back-office Kergui' },
  robots: { index: false, follow: false },
};

export const dynamic = 'force-dynamic';

export default function AdminRoot({ children }: { children: React.ReactNode }) {
  return <div className="admin">{children}</div>;
}
