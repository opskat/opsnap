import { CircleCheck } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { Panel } from "./Panel";

function StepRow({
  index,
  title,
  description,
  right,
}: {
  index: number | "check";
  title: string;
  description: string;
  right: ReactNode;
}) {
  return (
    <li className="flex flex-wrap items-center justify-between gap-4 rounded-md border bg-muted/30 px-5 py-4">
      <div className="flex min-w-0 items-start gap-3">
        <span
          className={cn(
            "mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold",
            index === "check" ? "text-success" : "bg-accent text-muted-foreground"
          )}
        >
          {index === "check" ? <CircleCheck className="size-5" aria-hidden /> : index}
        </span>
        <div className="min-w-0">
          <p className="font-medium">{title}</p>
          <p className="text-sm text-muted-foreground">{description}</p>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-3">{right}</div>
    </li>
  );
}

function DatasourceOrStorageStep({
  index,
  title,
  description,
  done,
  doneText,
  addLabel,
  viewLabel,
  to,
}: {
  index: number;
  title: string;
  description: string;
  done: boolean;
  doneText: string;
  addLabel: string;
  viewLabel: string;
  to: string;
}) {
  return (
    <StepRow
      index={done ? "check" : index}
      title={title}
      description={description}
      right={
        <>
          {done && <span className="text-sm text-success">{doneText}</span>}
          <Button asChild variant={done ? "outline" : "default"}>
            <Link to={to}>{done ? viewLabel : addLabel}</Link>
          </Button>
        </>
      }
    />
  );
}

/** 空状态的“开始第一次备份”三步引导（spec「概览」→「空状态」）：一个任务都没有时代替最近运行 / 14 天运行 / 存储目标 */
export function StartGuide({ datasources, storages }: { datasources: number; storages: number }) {
  const { t } = useTranslation();
  const hasDatasources = datasources > 0;
  const hasStorages = storages > 0;
  const ready = hasDatasources && hasStorages;

  return (
    <Panel title={t("overview.guide.title")} loading={false}>
      <div className="flex flex-col gap-4 px-5 pb-5">
        <p className="text-sm text-muted-foreground">{t("overview.guide.description")}</p>
        <ol className="flex flex-col gap-3">
          <DatasourceOrStorageStep
            index={1}
            title={t("overview.guide.step1.title")}
            description={t("overview.guide.step1.description")}
            done={hasDatasources}
            doneText={t("overview.guide.step1.done", { count: datasources })}
            addLabel={t("overview.guide.step1.add")}
            viewLabel={t("overview.guide.step1.view")}
            to="/sources"
          />
          <DatasourceOrStorageStep
            index={2}
            title={t("overview.guide.step2.title")}
            description={t("overview.guide.step2.description")}
            done={hasStorages}
            doneText={t("overview.guide.step2.done", { count: storages })}
            addLabel={t("overview.guide.step2.add")}
            viewLabel={t("overview.guide.step2.view")}
            to="/storage"
          />
          <StepRow
            index={3}
            title={t("overview.guide.step3.title")}
            description={t("overview.guide.step3.description")}
            right={
              <>
                {!ready && <span className="text-sm text-muted-foreground">{t("overview.guide.step3.note")}</span>}
                {ready ? (
                  <Button asChild>
                    <Link to="/jobs/new">{t("jobs.list.create")}</Link>
                  </Button>
                ) : (
                  <Button disabled>{t("jobs.list.create")}</Button>
                )}
              </>
            }
          />
        </ol>
      </div>
    </Panel>
  );
}
