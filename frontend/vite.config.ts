/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

// 开发时把 /api 转发给本机运行的 opsnap（默认 127.0.0.1:8080），可用 OPSNAP_API 覆盖
const apiTarget = process.env.OPSNAP_API ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: false },
    },
  },
  preview: {
    proxy: {
      "/api": { target: apiTarget, changeOrigin: false },
    },
  },
  test: {
    environment: "happy-dom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    // Node 22+ 自带 localStorage（需 --localstorage-file），关掉它，用 happy-dom 的实现
    execArgv: ["--no-experimental-webstorage"],
  },
});
