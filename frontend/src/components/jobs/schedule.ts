import type { TFunction } from "i18next";

import type { JobSchedule } from "@/lib/jobs";

const pad = (n: number) => String(n).padStart(2, "0");

/**
 * 计划的一句话描述，例如“每天 02:00”“每小时第 15 分钟”“每周一、三 09:00”“Cron：0 2 * * *”。
 * 与向导第 5 步的摘要共用同一份译文（jobs.wizard.confirm.schedule*），任务列表与详情页复用本函数。
 */
export function scheduleDescription(t: TFunction, language: string, schedule: JobSchedule): string {
  const time = `${pad(schedule.hour)}:${pad(schedule.minute)}`;
  if (schedule.kind === "hourly") return t("jobs.wizard.confirm.scheduleHourly", { minute: pad(schedule.minute) });
  if (schedule.kind === "daily") return t("jobs.wizard.confirm.scheduleDaily", { time });
  if (schedule.kind === "weekly") {
    const sep = language.toLowerCase().startsWith("zh") ? "、" : ", ";
    const weekdays = [...schedule.weekdays]
      .sort((a, b) => a - b)
      .map((d) => t(`jobs.wizard.schedule.weekday.${d}`))
      .join(sep);
    return t("jobs.wizard.confirm.scheduleWeekly", { weekdays, time });
  }
  return t("jobs.wizard.confirm.scheduleCron", { cron: schedule.cron });
}
