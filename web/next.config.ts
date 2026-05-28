import type { NextConfig } from 'next';

const config: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  experimental: {
    typedRoutes: true,
  },
  images: {
    remotePatterns: [
      { protocol: 'https', hostname: 'uselemon.com' },
      { protocol: 'https', hostname: 'classpass-res.cloudinary.com' },
      { protocol: 'https', hostname: 'a0.muscache.com' },
    ],
  },
};

export default config;
