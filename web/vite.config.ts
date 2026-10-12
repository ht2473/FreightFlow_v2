import { writeFileSync } from "node:fs";
import { fileURLToPath, URL } from "node:url";
import babel from "@rolldown/plugin-babel";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

// Каталог сборки хранится в репозитории пустым: go:embed требует, чтобы он существовал.
const keepDist: Plugin = {
  name: "keep-dist",
  apply: "build",
  writeBundle(options) {
    if (options.dir) {
      writeFileSync(`${options.dir}/.gitkeep`, "");
    }
  },
};

// Запросы к серверу приложения в разработке: API, страницы входа, карта и общая статика.
const backend = "http://localhost:8080";
const proxied = [
  "/api",
  "/login",
  "/logout",
  "/setup-2fa",
  "/invite",
  "/password",
  "/static",
  // Ресурсы карты; сама страница /map — маршрут SPA.
  "^/map/",
  "^/tiles/",
  "/healthz",
  "/readyz",
];

export default defineConfig({
  plugins: [react(), babel({ presets: [reactCompilerPreset()] }), keepDist],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: Object.fromEntries(proxied.map((p) => [p, { target: backend, changeOrigin: false }])),
  },
  build: {
    manifest: true,
    sourcemap: true,
    target: "es2023",
    rolldownOptions: {
      input: {
        app: fileURLToPath(new URL("./index.html", import.meta.url)),
        server: fileURLToPath(new URL("./src/styles/server.css", import.meta.url)),
      },
      output: {
        // Библиотеки отдельно от кода приложения: они меняются реже и дольше живут в кеше.
        codeSplitting: {
          groups: [
            { name: "react", test: /node_modules[/](react|react-dom|scheduler)[/]/, priority: 30 },
            {
              name: "aria",
              test: /node_modules[/](react-aria|react-stately|@react-aria|@react-stately|@internationalized|react-aria-components)[/]/,
              priority: 20,
            },
            { name: "vendor", test: /node_modules[/]/, priority: 10 },
          ],
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: false,
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["./src/test/setup.ts"],
  },
});
