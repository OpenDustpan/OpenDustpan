import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The console is a static build. In development, /api is proxied to a local
// OpenDustpan server so the console and API share an origin.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
  build: {
    sourcemap: true,
  },
});
