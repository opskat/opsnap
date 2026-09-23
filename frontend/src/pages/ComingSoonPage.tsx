import { useTranslation } from "react-i18next";

import { PageHeader } from "@/components/layout/PageHeader";

/** 尚未实现的导航页面，保持导航完整；对应功能落地时直接替换 */
export function ComingSoonPage({ title }: { title: string }) {
  const { t } = useTranslation();
  return (
    <>
      <PageHeader title={t(title)} />
      <p className="px-8 py-6 text-[13px] text-muted-foreground">{t("common.comingSoon")}</p>
    </>
  );
}
