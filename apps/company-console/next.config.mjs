const config = {
  poweredByHeader: false,
  async rewrites() {
    return [
      {
        source: "/v1/:path*",
        destination: `${process.env.API_INTERNAL_URL ?? "http://127.0.0.1:8080"}/v1/:path*`,
      },
    ];
  },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "Referrer-Policy", value: "no-referrer" },
          {
            key: "Content-Security-Policy",
            value: "frame-ancestors 'none'; object-src 'none'; base-uri 'self'",
          },
        ],
      },
    ];
  },
};
export default config;
