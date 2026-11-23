import type { Metadata, Viewport } from 'next';
import './globals.css';
import { site } from '../lib/site';

export const metadata: Metadata = {
  metadataBase: new URL(site.url),
  title: { default: site.title, template: '%s · Kergui' },
  description: site.description,
  applicationName: site.name,
  keywords: [
    'box fibre',
    'Wi-Fi',
    'qui est connecté à mon Wi-Fi',
    'bloquer un appareil',
    'contrôle parental',
    'ZTE F660',
    'ZTE F680',
    'F6600P',
    'Funbox',
    'Sénégal',
    'Dakar',
  ],
  alternates: { canonical: '/' },
  openGraph: {
    type: 'website',
    url: '/',
    siteName: site.name,
    title: site.title,
    description: site.description,
    locale: site.locale,
  },
  twitter: { card: 'summary_large_image', title: site.title, description: site.description },
  robots: { index: true, follow: true },
  formatDetection: { telephone: false },
};

export const viewport: Viewport = {
  themeColor: [
    { media: '(prefers-color-scheme: light)', color: '#0B6E4F' },
    { media: '(prefers-color-scheme: dark)', color: '#0B1F18' },
  ],
  width: 'device-width',
  initialScale: 1,
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="fr">
      <body>{children}</body>
    </html>
  );
}
