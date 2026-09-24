import { Check, Copy, Download } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { downloadText, keyFileContent, keyFileName, type StorageLocation } from "@/lib/storage";

/** 复制密钥与下载密钥文件两个按钮；设置密钥与查看密钥共用 */
export function KeyActions({
  name,
  location,
  keyText,
  fingerprint,
}: {
  name: string;
  location: StorageLocation;
  keyText: string;
  fingerprint: string;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    await navigator.clipboard.writeText(keyText);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };

  const download = () =>
    downloadText(
      keyFileName(name),
      keyFileContent(
        {
          title: t("storage.keyFile.title"),
          storage: t("storage.keyFile.storage"),
          location: t("storage.keyFile.location"),
          fingerprint: t("storage.keyFile.fingerprint"),
          command: t("storage.keyFile.command"),
        },
        { name, location, key: keyText, fingerprint }
      )
    );

  return (
    <>
      <Button type="button" variant="outline" size="sm" onClick={() => void copy()}>
        {copied ? <Check /> : <Copy />}
        {copied ? t("common.copied") : t("common.copy")}
      </Button>
      <Button type="button" variant="outline" size="sm" onClick={download}>
        <Download />
        {t("storage.key.download")}
      </Button>
    </>
  );
}
