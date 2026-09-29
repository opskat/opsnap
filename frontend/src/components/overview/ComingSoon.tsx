import { useId, type ReactElement } from "react";
import { useTranslation } from "react-i18next";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";

/**
 * 本轮做不了的入口（spec 决策 1）：显示但禁用，悬停或键盘聚焦时提示“后续版本支持”。
 * 用 aria-disabled 而不是 disabled，使它仍能被键盘聚焦，并始终以描述读出“后续版本支持”。
 * 子元素不应带 onClick。
 */
export function ComingSoon({ children }: { children: ReactElement }) {
  const { t } = useTranslation();
  const id = useId();
  return (
    <Tooltip>
      <TooltipTrigger asChild aria-disabled="true" aria-describedby={id}>
        {children}
      </TooltipTrigger>
      <span id={id} className="sr-only">
        {t("overview.comingSoon")}
      </span>
      <TooltipContent>{t("overview.comingSoon")}</TooltipContent>
    </Tooltip>
  );
}

/** 禁用外观：与 disabled 相同的淡化，但保留焦点 */
export const comingSoonClass = "cursor-not-allowed opacity-50";
