import type { NextConfig } from "next";

const previewDistDir = process.env.OPEN_REVIEW_NEXT_DIST_DIR?.trim();

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  // Keep self-hosted image builds within a small Docker VM's memory budget.
  experimental: {
    cpus: 2,
    webpackMemoryOptimizations: true,
  },
  // The documented local console address uses 127.0.0.1 while Next's dev
  // server resolves HMR through localhost. This keeps both loopback origins
  // explicit without broadening production request handling.
  allowedDevOrigins: ["127.0.0.1"],
  // A local verification server must not reuse the primary developer server's
  // `.next/dev` lock. This remains opt-in and only affects the build output;
  // it never changes the deployed image or runtime routing.
  ...(previewDistDir ? { distDir: previewDistDir } : {}),
  async headers() {
    return [
      {
        source: "/invitations/accept",
        headers: [{ key: "Referrer-Policy", value: "no-referrer" }],
      },
    ];
  },
};

export default nextConfig;
