import { useTranslation } from "react-i18next";

import { PageHeader } from "@/components/layout/PageHeader";
import { AccountCard } from "@/components/settings/AccountCard";
import { ApiTokensCard } from "@/components/settings/ApiTokensCard";
import { LoginMethodsCard } from "@/components/settings/LoginMethodsCard";

export function SettingsPage() {
  const { t } = useTranslation();
  return (
    <>
      <PageHeader title={t("nav.settings")} subtitle={t("settings.subtitle")} />
      <section className="flex flex-col gap-6 px-8 py-6">
        <AccountCard />
        <LoginMethodsCard />
        <ApiTokensCard />
      </section>
    </>
  );
}
