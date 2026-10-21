// 密钥页用到的纯函数：列表里「公钥 / 密码」列的取字、从私钥算出公钥与指纹、
// 公钥块该显示什么、下载文件名，以及不静默的剪贴板写入。
import { describe, expect, it, vi } from "vitest";
import { ApiError, type CredentialItem } from "./api";
import {
  fingerprintOfPublicKey,
  publicKeyFilename,
  publicKeyOfPrivateKey,
  publicKeyView,
  secretPreview,
  writeClipboard,
} from "./credentials";

// 用 ssh-keygen -t ed25519 -C ops@opsnap 生成的测试密钥（无口令）
const KEY = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACC21/7mlnnFBzPTiGvO2AlnooSlSI0K77MsRuwRWkZo9wAAAJAFdA5YBXQO
WAAAAAtzc2gtZWQyNTUxOQAAACC21/7mlnnFBzPTiGvO2AlnooSlSI0K77MsRuwRWkZo9w
AAAEB6drs9KgBWIHA9QfMvxRxqc22Vu4C6v4VbPy5Vjd1DmrbX/uaWecUHM9OIa87YCWei
hKVIjQrvsyxG7BFaRmj3AAAACm9wc0BvcHNuYXABAgM=
-----END OPENSSH PRIVATE KEY-----
`;
const PUB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILbX/uaWecUHM9OIa87YCWeihKVIjQrvsyxG7BFaRmj3";
// 同一把钥匙，但私钥带口令；公钥在 OpenSSH 格式里位于加密段之外，带口令也能读出来
const ENCRYPTED_KEY = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAACmFlczI1Ni1jdHIAAAAGYmNyeXB0AAAAGAAAABDGcsTawh
xHX5v5knaJyqsMAAAAGAAAAAEAAAAzAAAAC3NzaC1lZDI1NTE5AAAAIPDP09U4VAEsEpBd
be6lwLe4mudG0QdYvC30GgpfSsWoAAAAkEFFAo+/Im3vRQvHu3og5CBpw5OeDyNDdSXsu3
DPfbaIIXF4cdEv649KJXYc9QflHwNudtKRq4Mx+FuS8df7hB6xY9mbqr4pTMBSdbUohCxo
hHAK1K7K45bZ2mZa63Hvq9yFnZdk8G+809fqafDkS1OIENu8gpThBiL0wJfxun0r/cELod
pi/Mag6blcRHh/MQ==
-----END OPENSSH PRIVATE KEY-----
`;
const ENCRYPTED_PUB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPDP09U4VAEsEpBdbe6lwLe4mudG0QdYvC30GgpfSsWo";

const NOW = 1_790_000_000;
function item(patch: Partial<CredentialItem> = {}): CredentialItem {
  return {
    id: 1,
    name: "ops 私钥",
    kind: "ssh_key",
    username: "ops",
    note: "",
    has_secret: true,
    has_passphrase: false,
    public_key: PUB,
    servers: [],
    created_at: NOW,
    updated_at: NOW,
    ...patch,
  };
}

describe("列表「公钥 / 密码」列", () => {
  it("私钥显示公钥明文，密码只显示掩码", () => {
    expect(secretPreview(item())).toBe(PUB);
    expect(secretPreview(item({ kind: "password", public_key: "" }))).toBe("密码 ·••••••••");
  });

  it("没有内容时为 null（界面显示「—」）", () => {
    expect(secretPreview(item({ has_secret: false }))).toBeNull();
    // 私钥有内容但服务端没给公钥：不拿别的东西冒充
    expect(secretPreview(item({ public_key: "" }))).toBeNull();
  });
});

describe("从私钥算公钥", () => {
  it("无口令的 OpenSSH 私钥", () => {
    expect(publicKeyOfPrivateKey(KEY)).toEqual({ ok: true, publicKey: PUB });
  });

  it("带口令的私钥同样读得出公钥（公钥在加密段之外）", () => {
    expect(publicKeyOfPrivateKey(ENCRYPTED_KEY)).toEqual({ ok: true, publicKey: ENCRYPTED_PUB });
  });

  it("不是私钥时给出格式不对", () => {
    expect(publicKeyOfPrivateKey("ssh-ed25519 AAAA 随便粘的")).toEqual({ ok: false, reason: "format" });
    expect(publicKeyOfPrivateKey("")).toEqual({ ok: false, reason: "format" });
  });

  it("前端不认识的私钥格式不硬猜", () => {
    const rsa = "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----";
    expect(publicKeyOfPrivateKey(rsa)).toEqual({ ok: false, reason: "unsupported" });
  });
});

describe("公钥指纹", () => {
  it("与 ssh-keygen -lf 一致：SHA256: + base64，去掉尾部等号", async () => {
    await expect(fingerprintOfPublicKey(PUB)).resolves.toBe("SHA256:ftIdw6zfbn+bO8ujt77rHl2m2F8DFzrvpOzGpzdn4Ww");
  });

  it("算不出来时为 null，不造假", async () => {
    await expect(fingerprintOfPublicKey("ssh-ed25519 !!!")).resolves.toBeNull();
    await expect(fingerprintOfPublicKey("")).resolves.toBeNull();
  });
});

describe("公钥块", () => {
  it("新建且私钥为空：提示粘贴", () => {
    expect(publicKeyView({ item: null, privateKey: "" })).toEqual({ kind: "empty" });
  });

  it("编辑时私钥框留空：显示已保存的公钥", () => {
    expect(publicKeyView({ item: item(), privateKey: "  " })).toEqual({ kind: "saved", publicKey: PUB });
  });

  it("粘贴合法私钥：显示由它算出的公钥", () => {
    expect(publicKeyView({ item: null, privateKey: KEY })).toEqual({ kind: "derived", publicKey: PUB });
    // 编辑时粘贴了新私钥，就不再显示已保存的那一条
    expect(publicKeyView({ item: item(), privateKey: KEY })).toEqual({ kind: "derived", publicKey: PUB });
  });

  it("粘贴的内容不像私钥时提示格式不对，不显示旧公钥", () => {
    expect(publicKeyView({ item: item(), privateKey: "hello" })).toEqual({ kind: "invalid" });
  });

  it("前端算不出公钥的格式单独标明，不谎称格式不对", () => {
    const rsa = "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----";
    expect(publicKeyView({ item: item(), privateKey: rsa })).toEqual({ kind: "unsupported" });
  });
});

describe("下载 .pub", () => {
  it("文件名用密钥名，非法字符换成短横线", () => {
    expect(publicKeyFilename("ops 私钥")).toBe("ops 私钥.pub");
    expect(publicKeyFilename("prod/root:2026")).toBe("prod-root-2026.pub");
  });

  it("还没有名称时用 id_ed25519.pub", () => {
    expect(publicKeyFilename("  ")).toBe("id_ed25519.pub");
  });
});

describe("写剪贴板", () => {
  it("把 resolve 取到的内容写进剪贴板", async () => {
    const write = vi.spyOn(navigator.clipboard, "writeText");
    await expect(writeClipboard(() => "SECRET-PW-1")).resolves.toEqual({ ok: true });
    expect(write).toHaveBeenCalledWith("SECRET-PW-1");
  });

  it("接口失败时带回原因", async () => {
    await expect(writeClipboard(() => Promise.reject(new ApiError(500, 12000, "数据库繁忙")))).resolves.toEqual({
      ok: false,
      error: "复制失败：数据库繁忙",
    });
  });

  it("浏览器拒绝写剪贴板时说明原因，不静默", async () => {
    vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));
    await expect(writeClipboard(() => "x")).resolves.toEqual({
      ok: false,
      error: "复制失败：浏览器不允许访问剪贴板，请手动选中后复制",
    });
  });

  it("没有内容时也报错", async () => {
    await expect(writeClipboard(() => "")).resolves.toEqual({ ok: false, error: "复制失败：没有可复制的内容" });
  });
});
