import { describe, expect, it } from "vitest";

import {
  emptyLocation,
  keyFileContent,
  keyFileName,
  kopiaConnectCommand,
  parseKeyFile,
  s3Location,
} from "@/lib/storage";

const labels = { title: "T", storage: "S", location: "L", fingerprint: "F", command: "C" };

describe("密钥文件", () => {
  it("文件名为 opsnap-<存储名>-key.txt，路径字符与空白替换为连字符", () => {
    expect(keyFileName("本地备份")).toBe("opsnap-本地备份-key.txt");
    expect(keyFileName("MinIO / prod")).toBe("opsnap-MinIO-prod-key.txt");
  });

  it("包含密钥、指纹、位置与 kopia 命令，并能从中取回密钥（含空格的自设密码）", () => {
    const key = "my own pass phrase";
    const text = keyFileContent(labels, {
      name: "a",
      location: { ...emptyLocation(), path: "/var/backups/opsnap" },
      key,
      fingerprint: "3F9A···C218",
    });
    expect(text).toContain("F: 3F9A···C218");
    expect(text).toContain("L: /var/backups/opsnap");
    expect(text).toContain("kopia repository connect filesystem --path /var/backups/opsnap");
    expect(parseKeyFile(text)).toBe(key);
  });

  it("只含密钥的文本文件：去掉首尾空白", () => {
    expect(parseKeyFile("  Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw\n\n")).toBe("Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw");
    expect(parseKeyFile("Key: abc\r\nother")).toBe("abc");
  });
});

describe("S3 位置", () => {
  it("最终位置：前缀去掉首尾斜杠后以斜杠结尾", () => {
    expect(s3Location("b", "/backups/")).toBe("s3://b/backups/");
    expect(s3Location("b", "")).toBe("s3://b/");
  });

  it("kopia 命令带上 Endpoint、前缀、HTTP 与跳过校验参数，Secret Key 用占位符", () => {
    const cmd = kopiaConnectCommand({
      ...emptyLocation(),
      kind: "s3",
      endpoint: "minio.lan:9000",
      bucket: "opsnap",
      prefix: "prod",
      access_key: "AK",
      secret_key: "real-secret",
      use_tls: false,
      skip_verify: true,
    });
    expect(cmd).toBe(
      "kopia repository connect s3 --endpoint minio.lan:9000 --bucket opsnap --prefix prod/ --access-key AK --secret-access-key <Secret Key> --disable-tls --disable-tls-verification"
    );
    expect(cmd).not.toContain("real-secret");
  });
});
