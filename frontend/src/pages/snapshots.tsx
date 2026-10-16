// 快照页：选择任务，左侧按时间列出快照（新增量、保留原因），右侧是文件树或数据库导出文件。
import * as React from "react";
import { Link, useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { PageTitle, Sheet, Skeleton, TypeTile } from "@/components/bits";
import { api, type JobItem, type SnapshotItem } from "@/lib/api";
import { fmtBytes } from "@/lib/format";
import { isFiles, sourceOf } from "@/lib/jobs";
import { REASON_LABEL, addedPercent, fmtSnapshotTime } from "@/lib/snapshots";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";
import { ExportsPanel, FilesPanel } from "./snapshot-panels";
import { LoadError } from "./settings";

export function SnapshotsPage() {
  const [params, setParams] = useSearchParams();
  const loadJobs = React.useCallback(async () => (await api.jobs.list("all")).list ?? [], []);
  const { data: jobs, error, loading, reload } = useLoad(loadJobs);

  const wanted = Number(params.get("job")) || 0;
  const job = jobs?.find((j) => j.id === wanted) ?? jobs?.[0];

  return (
    <>
      <PageTitle title="快照" />
      {error && !jobs ? (
        <LoadError what="任务" message={error} onRetry={reload} />
      ) : loading && !jobs ? (
        <Skeleton className="h-11 w-72" />
      ) : !jobs || jobs.length === 0 || !job ? (
        <Sheet className="px-6 py-14 text-center">
          <h2 className="text-[15px] font-semibold">还没有备份任务</h2>
          <p className="mt-1.5 text-ink-2">创建任务并运行一次后，快照会出现在这里。</p>
          <Button asChild className="mt-5">
            <Link to="/jobs/new">创建任务</Link>
          </Button>
        </Sheet>
      ) : (
        <SnapshotBrowser
          key={job.id}
          job={job}
          picker={
            <Select
              value={String(job.id)}
              onValueChange={(v) => {
                const next = new URLSearchParams(params);
                next.set("job", v);
                setParams(next, { replace: true });
              }}
            >
              <SelectTrigger className="h-11 w-72 bg-surface" aria-label="选择任务">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {jobs.map((j) => (
                  <SelectItem key={j.id} value={String(j.id)}>
                    <span className="flex items-center gap-2.5">
                      <TypeTile type={sourceOf(j.source_type).tile} size="sm" />
                      {j.name}
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          }
        />
      )}
    </>
  );
}

/** 一个任务的快照：任务选择器、左侧列表、右侧内容。换任务时由 key 重建 */
function SnapshotBrowser({ job, picker }: { job: JobItem; picker: React.ReactNode }) {
  const load = React.useCallback(() => api.snapshots.list(job.id), [job.id]);
  const { data, error, loading, reload } = useLoad(load);
  const [selectedId, setSelectedId] = React.useState("");
  const snapshots = data?.snapshots ?? [];
  const snap = snapshots.find((s) => s.id === selectedId) ?? snapshots[0];

  let body: React.ReactNode;
  if (error && !data) {
    body = <LoadError what="快照" message={error} onRetry={reload} />;
  } else if (loading && !data) {
    body = (
      <div className="grid gap-6 lg:grid-cols-[340px_1fr]" aria-busy>
        <div className="flex flex-col gap-2">
          {[0, 1, 2, 3, 4].map((i) => (
            <Skeleton key={i} className="h-14" />
          ))}
        </div>
        <Skeleton className="h-[420px]" />
      </div>
    );
  } else if (!snap) {
    body = (
      <Sheet className="px-6 py-14 text-center">
        <h2 className="text-[15px] font-semibold">这个任务还没有快照</h2>
        <p className="mt-1.5 text-ink-2">任务运行成功后会留下快照。</p>
      </Sheet>
    );
  } else {
    body = (
      <div className="grid gap-6 lg:grid-cols-[340px_1fr]">
        <SnapshotList snapshots={snapshots} selectedId={snap.id} onSelect={setSelectedId} />
        {isFiles(job.source_type) ? (
          <FilesPanel key={snap.id} jobId={job.id} snapshot={snap} />
        ) : (
          <ExportsPanel key={snap.id} jobId={job.id} snapshot={snap} />
        )}
      </div>
    );
  }

  return (
    <>
      <div className="mb-6 flex flex-wrap items-center gap-4">
        {picker}
        {snap && (
          <span className="text-[13px] text-ink-2">
            {snapshots.length} 个快照，存在 {job.repo_name}
          </span>
        )}
      </div>
      {body}
    </>
  );
}

function SnapshotList({
  snapshots,
  selectedId,
  onSelect,
}: {
  snapshots: SnapshotItem[];
  selectedId: string;
  onSelect: (id: string) => void;
}) {
  const max = Math.max(0, ...snapshots.map((s) => s.bytes_added));
  return (
    <div>
      <div className="mb-2 flex justify-between px-1 text-xs text-ink-3">
        <span>时间</span>
        <span>这次新增的数据</span>
      </div>
      <ul className="flex flex-col gap-1" role="listbox" aria-label="快照">
        {snapshots.map((s) => {
          const on = s.id === selectedId;
          return (
            <li key={s.id}>
              <button
                type="button"
                role="option"
                aria-selected={on}
                onClick={() => onSelect(s.id)}
                className={cn(
                  "w-full rounded-lg border border-transparent px-3 py-2.5 text-left hover:bg-surface",
                  on && "border-line-strong bg-surface shadow-[inset_3px_0_0_var(--brand)]"
                )}
              >
                <div className="flex items-baseline justify-between gap-3">
                  <span className="text-[13.5px] font-medium num">{fmtSnapshotTime(s.time)}</span>
                  <span className="text-xs text-ink-2 num">
                    {s.bytes_added >= 0 ? `+${fmtBytes(s.bytes_added)}` : "—"}
                  </span>
                </div>
                <div className="mt-1.5 flex items-center gap-3">
                  <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-sunken">
                    <span
                      data-testid="added-bar"
                      className={cn("block h-full rounded-full", on ? "bg-brand" : "bg-ink-3")}
                      style={{ width: `${addedPercent(s.bytes_added, max)}%` }}
                    />
                  </span>
                  <span className="flex min-w-8 justify-end gap-1.5 text-[11px] text-ink-3">
                    {s.reasons.map((r) => (
                      <span key={r}>{REASON_LABEL[r]}</span>
                    ))}
                  </span>
                </div>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
