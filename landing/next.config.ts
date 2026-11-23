import type { NextConfig } from 'next';

// A static site (out/): fast, cacheable, served by any web server — the
// Dockerfile uses nginx. No server code runs in production.
const nextConfig: NextConfig = {
  output: 'export',
  trailingSlash: true,
  images: { unoptimized: true },
  poweredByHeader: false,
};

export default nextConfig;
