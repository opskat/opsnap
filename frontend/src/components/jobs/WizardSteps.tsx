import { Check } from "lucide-react";
import { useTranslation } from "react-i18next";

import { WIZARD_STEPS } from "@/lib/jobs";
import { cn } from "@/lib/utils";

/** 向导顶部步骤条：当前步骤高亮；已完成（当前步骤之前）的步骤可以点击返回修改 */
export function WizardSteps({ current, onBack }: { current: number; onBack: (step: number) => void }) {
  const { t } = useTranslation();
  return (
    <nav aria-label={t("jobs.wizard.stepsLabel")}>
      <ol className="flex items-center gap-3">
        {WIZARD_STEPS.map((key, i) => {
          const done = i < current;
          const active = i === current;
          const content = (
            <>
              <span
                className={cn(
                  "flex size-5 shrink-0 items-center justify-center rounded-full border font-mono text-2xs",
                  active && "border-primary bg-primary text-primary-foreground",
                  done && "border-success text-success",
                  !active && !done && "text-muted-foreground"
                )}
              >
                {done ? <Check aria-hidden className="size-3" /> : i + 1}
              </span>
              <span className={cn("text-sm", active ? "font-semibold" : "text-muted-foreground")}>
                {t(`jobs.wizard.steps.${key}`)}
              </span>
            </>
          );
          return (
            <li
              key={key}
              aria-current={active ? "step" : undefined}
              className="flex flex-1 items-center gap-2 last:flex-none"
            >
              {done ? (
                <button
                  type="button"
                  className="flex items-center gap-2 rounded-md hover:underline"
                  onClick={() => onBack(i)}
                >
                  {content}
                </button>
              ) : (
                content
              )}
              {i < WIZARD_STEPS.length - 1 && <span aria-hidden className="ml-1 h-px flex-1 bg-border" />}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
