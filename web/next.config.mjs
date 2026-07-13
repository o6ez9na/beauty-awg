/** @type {import('next').NextConfig} */
const backend = process.env.BACKEND_URL || "http://localhost:8099";

const nextConfig = {
  output: "standalone",
  // Proxy /api to the Go backend so the browser stays same-origin (cookies work).
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
  },
};

export default nextConfig;
