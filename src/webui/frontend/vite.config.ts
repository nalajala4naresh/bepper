import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Builds into ../static/dist, which src/webui/webui.go serves via go:embed.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../static/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
});
