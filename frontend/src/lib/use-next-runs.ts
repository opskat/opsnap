import * as React from "react";
import { api, errorMessage, type JobSchedule } from "./api";
import { fieldErrorsOf } from "./jobs";

export interface NextRuns {
  /** Unix 秒；预览还没回来或计划无效时为 null */
  times: number[] | null;
  /** 计划无效等原因，可直接显示在字段下方 */
  error: string | null;
}

/**
 * 预览正在编辑的计划接下来几次的运行时间。改动后稍等片刻再请求；
 * 回来之前保持 null，避免显示上一份计划的时间。
 */
export function useNextRuns(schedule: JobSchedule): NextRuns {
  const sig = JSON.stringify(schedule);
  const [res, setRes] = React.useState<{ sig: string } & NextRuns>({ sig: "", times: null, error: null });

  React.useEffect(() => {
    let cancelled = false;
    const timer = setTimeout(() => {
      api.jobs
        .nextRuns(JSON.parse(sig) as JobSchedule, 5)
        .then((r) => !cancelled && setRes({ sig, times: r.times ?? [], error: null }))
        .catch((err) => {
          if (cancelled) return;
          const fields = fieldErrorsOf(err);
          setRes({ sig, times: null, error: fields ? Object.values(fields).flat().join("；") : errorMessage(err) });
        });
    }, 300);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [sig]);

  return res.sig === sig ? res : { times: null, error: null };
}
