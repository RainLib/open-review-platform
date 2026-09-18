import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  // The documented local console address uses 127.0.0.1 while Next's dev
  // server resolves HMR through localhost. This keeps both loopback origins
  // explicit without broadening production request handling.
  allowedDevOrigins: ["127.0.0.1"],
};

export default nextConfig;
