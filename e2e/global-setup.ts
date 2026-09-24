// 冒烟 e2e 的被测服务：在临时目录生成配置，用专用端口启动已构建的 bin/opsnap；
// 从启动日志中读取设置码，通过环境变量 OPSNAP_SETUP_CODE 交给用例（首次设置由 setup 项目在界面上完成）。
// 返回的函数作为 teardown，停止进程并删除临时数据。不读取 .env，不连接任何真实环境。
import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

import { FAKE_IDP_CLIENT_ID, FAKE_IDP_CLIENT_SECRET, FAKE_IDP_ISSUER, FAKE_IDP_PORT, SMOKE_PORT } from "./ports";

const root = resolve(import.meta.dirname, "..");
const healthURL = `http://127.0.0.1:${SMOKE_PORT}/api/v1/system/health`;

async function waitHealthy(timeoutMs: number) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(healthURL);
      const body = await res.json();
      // 确认是 OpsNap 的健康检查响应，端口被其他程序占用时不会误判
      if (res.ok && body?.code === 0 && body?.data?.database === "ok") return;
    } catch {
      // 尚未就绪
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`OpsNap 未在 ${timeoutMs}ms 内就绪：${healthURL}`);
}

async function waitURL(url: string, timeoutMs: number) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      if ((await fetch(url)).ok) return;
    } catch {
      // 尚未就绪
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`${url} 未在 ${timeoutMs}ms 内就绪`);
}

export default async function globalSetup() {
  const bin = join(root, "bin/opsnap");
  if (!existsSync(bin)) throw new Error("未找到 bin/opsnap，请通过 make e2e 运行（会先构建）");
  try {
    await fetch(healthURL);
    throw new Error(`端口 ${SMOKE_PORT} 已被占用，冒烟 e2e 需要独占该端口`);
  } catch (err) {
    if (err instanceof Error && err.message.includes("已被占用")) throw err;
  }

  const dir = mkdtempSync(join(tmpdir(), "opsnap-e2e-"));
  writeFileSync(
    join(dir, "config.yaml"),
    `env: TEST
source: file
debug: false
http:
  address:
    - "127.0.0.1:${SMOKE_PORT}"
logger:
  level: warn
  disableConsole: false
db:
  driver: sqlite
  dsn: "${join(dir, "opsnap.db")}?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
`
  );
  const idpBin = join(root, "bin/fakeidp");
  if (!existsSync(idpBin)) throw new Error("未找到 bin/fakeidp，请通过 make e2e 运行（会先构建）");
  const idp = spawn(
    idpBin,
    [
      "-addr",
      `127.0.0.1:${FAKE_IDP_PORT}`,
      "-issuer",
      FAKE_IDP_ISSUER,
      "-client-id",
      FAKE_IDP_CLIENT_ID,
      "-client-secret",
      FAKE_IDP_CLIENT_SECRET,
    ],
    { stdio: "inherit" }
  );
  const child = spawn(bin, ["-c", join(dir, "config.yaml")], { stdio: ["ignore", "pipe", "inherit"] });
  const exited = new Promise((r) => child.once("exit", r));
  const setupCode = new Promise<string>((resolveCode) => {
    let buffered = "";
    child.stdout.on("data", (chunk: Buffer) => {
      process.stdout.write(chunk);
      buffered += chunk.toString();
      const m = buffered.match(/OpsNap 设置码：([A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4})/);
      if (m) resolveCode(m[1]);
    });
  });

  const teardown = async () => {
    idp.kill("SIGTERM");
    child.kill("SIGTERM");
    await Promise.race([exited, new Promise((r) => setTimeout(r, 5000))]);
    if (child.exitCode === null) child.kill("SIGKILL");
    rmSync(dir, { recursive: true, force: true });
  };

  try {
    await waitHealthy(30_000);
    await waitURL(`${FAKE_IDP_ISSUER}/.well-known/openid-configuration`, 10_000);
    process.env.OPSNAP_SETUP_CODE = await Promise.race([
      setupCode,
      new Promise<string>((_, reject) => setTimeout(() => reject(new Error("启动日志中未找到设置码")), 5000)),
    ]);
  } catch (err) {
    await teardown();
    throw err;
  }
  return teardown;
}
