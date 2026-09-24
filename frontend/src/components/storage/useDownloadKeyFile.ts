import { useTranslation } from "react-i18next";

import { downloadText, keyFileContent, keyFileName, type StorageLocation } from "@/lib/storage";

/** 按界面语言生成并下载密钥文件；下载按钮与“验证后直接下载”共用 */
export function useDownloadKeyFile() {
  const { t } = useTranslation();
  return (info: { name: string; location: StorageLocation; key: string; fingerprint: string }) =>
    downloadText(
      keyFileName(info.name),
      keyFileContent(
        {
          title: t("storage.keyFile.title"),
          storage: t("storage.keyFile.storage"),
          location: t("storage.keyFile.location"),
          fingerprint: t("storage.keyFile.fingerprint"),
          command: t("storage.keyFile.command"),
        },
        info
      )
    );
}
