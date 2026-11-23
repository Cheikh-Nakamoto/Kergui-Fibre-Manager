import type { NextConfig } from 'next';

// A small Node server (standalone): the landing page is still prerendered as
// static HTML at build time; only /admin (the back-office) and /compte (the customer area) render
// per request, server-side, to call the hub with the admin's session.
// One id per build. The site redeploys on every push to main: a tab opened
// before a deploy that submits a form after it would call a Server Action
// the new build no longer has ("Failed to find Server Action"). With a
// deployment id, Next sees the mismatch and reloads the page instead.
const deploymentId = process.env.NEXT_DEPLOYMENT_ID || `build-${Date.now().toString(36)}`;

const nextConfig: NextConfig = {
  output: 'standalone',
  deploymentId,
  images: { unoptimized: true },
  poweredByHeader: false,
  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
          { key: 'X-Frame-Options', value: 'DENY' },
        ],
      },
      { source: '/compte', headers: [{ key: 'X-Robots-Tag', value: 'noindex, nofollow' }, { key: 'Cache-Control', value: 'no-store' }] },
      { source: '/compte/:path*', headers: [{ key: 'X-Robots-Tag', value: 'noindex, nofollow' }, { key: 'Cache-Control', value: 'no-store' }] },
      { source: '/admin/:path*', headers: [{ key: 'X-Robots-Tag', value: 'noindex, nofollow' }, { key: 'Cache-Control', value: 'no-store' }] },
    ];
  },
};

export default nextConfig;
