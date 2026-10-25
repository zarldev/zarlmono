import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  server: { host: "127.0.0.1", strictPort: true },
  preview: { host: "127.0.0.1", strictPort: true },
  build:
    mode === "test"
      ? {
          outDir: "dist-test",
          rollupOptions: {
            input: { main: "index.html", harness: "tests/harness.html" },
          },
        }
      : {},
}));
