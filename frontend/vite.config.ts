import { writeFileSync } from "node:fs";
import { fileURLToPath, URL } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

// 构建产物由 Go 内嵌（internal/web），该目录在仓库中只保留 .gitkeep，清空后需要补回
const outDir = fileURLToPath(new URL("../internal/web/dist", import.meta.url));
const keepGitkeep: Plugin = {
  name: "opsnap-keep-gitkeep",
  closeBundle() {
    writeFileSync(`${outDir}/.gitkeep`, "");
  },
};

export default defineConfig({
  plugins: [react(), tailwindcss(), keepGitkeep],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: {
    outDir,
    emptyOutDir: true,
  },
  server: {
    // 开发时接口转发到本地后端（configs/config.yaml 的 http.address）
    proxy: { "/api": "http://127.0.0.1:8210" },
  },
  test: {
    globals: true,
    environment: "happy-dom",
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.test.{ts,tsx}", "scripts/**/*.test.mjs"],
  },
});
