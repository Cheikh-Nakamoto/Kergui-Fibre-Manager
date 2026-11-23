import type { NextConfig } from 'next';

// A small Node server (standalone): the landing page is still prerendered as
// static HTML at build time; only /admin (the back-office) renders per
// request, server-side, to call the hub with the admin's session.
const nextConfig: NextConfig = {
  output: 'standalone',
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
      { source: '/admin/:path*', headers: [{ key: 'X-Robots-Tag', value: 'noindex, nofollow' }, { key: 'Cache-Control', value: 'no-store' }] },
    ];
  },
};

export default nextConfig;
