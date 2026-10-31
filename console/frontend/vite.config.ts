import { defineConfig } from "vite";

// The console is served by the Go server under /console/ in production and at /
// in dev. A relative base makes the built asset URLs work under any prefix.
export default defineConfig({
  base: "./",
  server: {
    port: 5173,
    // During `npm run dev`, proxy API + WS calls to the Go server on :8080.
    proxy: {
      "/api": "http://localhost:8080",
      "/ws": { target: "ws://localhost:8080", ws: true },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
