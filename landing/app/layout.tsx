import type { Metadata, Viewport } from 'next';
import { Chakra_Petch, Inter, JetBrains_Mono } from 'next/font/google';
import './globals.css';
import { site } from '../lib/site';

// The three families of the design, self-hosted by next/font and exposed as
// CSS variables (--font-chakra, --font-inter, --font-jbmono) for every page.
const chakra = Chakra_Petch({
  subsets: ['latin'],
  weight: ['500', '600', '700'],
  style: ['normal', 'italic'],
  variable: '--font-chakra',
  display: 'swap',
});
const inter = Inter({ subsets: ['latin'], variable: '--font-inter', display: 'swap' });
const mono = JetBrains_Mono({ subsets: ['latin'], weight: ['500', '600'], variable: '--font-jbmono', display: 'swap' });

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
  themeColor: '#07140F',
  colorScheme: 'dark',
  width: 'device-width',
  initialScale: 1,
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="fr" className={`${chakra.variable} ${inter.variable} ${mono.variable}`}>
      <body>{children}</body>
    </html>
  );
}
