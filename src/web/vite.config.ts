import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true },
  // Dev-server proxy so `npm run dev` (port 5173) can talk to the Go panel on
  // 8443 without a separate reverse-proxy. The cookie is __Host- scoped, which
  // requires Secure; vite is told to serve the dev server over TLS self-signed.
  // Production builds are still embedded into the Go binary via web/embed.go.
  server: {
    proxy: {
      "/api": "https://localhost:8443",
      "/healthz": "https://localhost:8443",
    },
  },
});
