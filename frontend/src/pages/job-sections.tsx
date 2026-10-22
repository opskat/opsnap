// 任务表单的各个分区：数据源、连接、存到、计划与保留、高级。
import * as React from "react";
import { Check, ChevronDown, FolderOpen, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Choice, Field, Segmented, Sheet, TypeTile } from "@/components/bits";
import { RetentionField } from "@/components/retention-field";
import { PathPicker } from "./job-paths";
import { SECRET_MASKED, type JobCompression, type RepoItem, type ScheduleKind, type ServerItem } from "@/lib/api";
import { fmtBytes, fmtNext } from "@/lib/format";
import {
  COMPRESSION_LABEL,
  SCHEDULE_LABEL,
  SOURCES,
  WEEKDAYS,
  advancedSummary,
  fmtDuration,
  firstError,
  hasDatabaseList,
  isFiles,
  sourceOf,
  type FieldErrors,
  type JobForm,
} from "@/lib/jobs";
import { REPO_TYPE_LABEL } from "@/lib/repos";
import { RETENTION_LABEL, type RetentionPreset } from "@/lib/retention";
import type { NextRuns } from "@/lib/use-next-runs";
import { cn } from "@/lib/utils";
import { DatabasePicker } from "./job-databases";

export interface SectionProps {
  form: JobForm;
  set: (patch: Partial<JobForm>) => void;
  errs: FieldErrors;
}

export function Card({
  title,
  aside,
  children,
}: {
  title: string;
  aside?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Sheet className="p-5">
      <div className="mb-4 flex min-h-8 flex-wrap items-center justify-between gap-3">
        <h2 className="text-[15px] font-semibold">{title}</h2>
        {aside}
      </div>
      {children}
    </Sheet>
  );
}

/** 控件和错误提示的关联属性；错误说明的 id 约定为「控件 id-msg」，与 Field 一致 */
function errAria(id: string, error: string | null) {
  return error ? { id, "aria-describedby": `${id}-msg`, "aria-invalid": true as const } : { id };
}

function ErrText({ id, children }: { id: string; children: string | null }) {
  if (!children) return null;
  return (
    <span id={`${id}-msg`} className="text-xs text-fail">
      {children}
    </span>
  );
}

export function SourceCard({ form, set, errs }: SectionProps) {
  return (
    <Card title="数据源">
      <div className="flex flex-wrap gap-2" role="radiogroup" aria-label="数据类型">
        {SOURCES.map((s) => {
          const on = form.source_type === s.value;
          return (
            <button
              key={s.value}
              type="button"
              role="radio"
              aria-checked={on}
              onClick={() => {
                if (on) return;
                set({ source_type: s.value, conn_mode: "ssh", databases: [], all_databases: false });
              }}
              className={cn(
                "flex items-center gap-2 rounded-lg border border-line-strong bg-surface py-1.5 pr-3.5 pl-1.5 text-[13.5px] hover:border-ink-3",
                on && "border-brand font-medium shadow-[0_0_0_1px_var(--brand)]"
              )}
            >
              <TypeTile type={s.tile} size="sm" className={cn(on && "border-brand")} />
              {s.label}
            </button>
          );
        })}
      </div>
      <Field label="名称" error={firstError(errs, "name")} className="mt-5 max-w-sm">
        <Input
          value={form.name}
          onChange={(e) => set({ name: e.target.value })}
          placeholder={isFiles(form.source_type) ? "用户上传文件" : `${sourceOf(form.source_type).label} 备份`}
          className="bg-surface"
        />
      </Field>
    </Card>
  );
}

function serverHint(s: ServerItem | undefined): string | undefined {
  if (!s || !s.hops || s.hops.length === 0) return undefined;
  return `经 ${s.hops.map((h) => h.server_name || h.host).join(" → ")}`;
}

export function ConnectionCard({
  form,
  set,
  errs,
  servers,
  jobId,
}: SectionProps & { servers: ServerItem[]; jobId: number }) {
  const files = isFiles(form.source_type);
  const ssh = files || form.conn_mode === "ssh";
  const server = servers.find((s) => s.id === form.server_id);
  const serverErr = firstError(errs, "server_id");
  const addrErr = firstError(errs, "db_host", "db_port");
  const port = sourceOf(form.source_type).port;
  return (
    <Card
      title="连接"
      aside={
        !files && (
          <Segmented
            label="连接方式"
            value={form.conn_mode}
            onChange={(v) => set({ conn_mode: v })}
            options={[
              { value: "ssh", label: "经 SSH" },
              { value: "direct", label: "直连" },
            ]}
          />
        )
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {ssh && (
          <Field label="服务器" htmlFor="job-server" error={serverErr} hint={serverHint(server)}>
            <Select
              value={form.server_id > 0 ? String(form.server_id) : ""}
              onValueChange={(v) => set({ server_id: Number(v) })}
            >
              <SelectTrigger className="w-full bg-surface" {...errAria("job-server", serverErr)}>
                <SelectValue placeholder="选择服务器" />
              </SelectTrigger>
              <SelectContent>
                {servers.map((s) => (
                  <SelectItem key={s.id} value={String(s.id)}>
                    <span className="flex items-center gap-2">
                      <span
                        className={cn("size-1.5 rounded-full", s.status === "failed" ? "bg-fail-fill" : "bg-ok-fill")}
                      />
                      {s.name}
                      {s.status === "failed" && <span className="text-xs text-fail">连不上</span>}
                      {s.hops && s.hops.length > 0 && <span className="text-xs text-ink-3">经 {s.hops.length} 跳</span>}
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )}
        {!files && (
          <>
            <Field
              label="数据库地址"
              error={addrErr}
              hint={form.conn_mode === "ssh" ? "从这台服务器上看的地址，容器就填容器 IP" : undefined}
            >
              <Input
                value={form.db_addr}
                onChange={(e) => set({ db_addr: e.target.value })}
                placeholder={`172.17.0.2:${port}`}
                autoComplete="off"
                spellCheck={false}
                className="bg-surface font-mono"
              />
            </Field>
            <Field label="用户" error={firstError(errs, "db_user")}>
              <Input
                value={form.db_user}
                onChange={(e) => set({ db_user: e.target.value })}
                placeholder={form.source_type === "redis" ? "没有用户就留空" : "backup"}
                autoComplete="off"
                className="bg-surface"
              />
            </Field>
            <Field label="密码" error={firstError(errs, "db_password")}>
              <Input
                type="password"
                value={form.db_password}
                onChange={(e) => set({ db_password: e.target.value })}
                placeholder={form.db_password_saved ? `${SECRET_MASKED}，留空保持不变` : undefined}
                autoComplete="new-password"
                className="bg-surface"
              />
            </Field>
            {form.source_type === "mongodb" && (
              <Field label="认证库" error={firstError(errs, "auth_db")}>
                <Input
                  value={form.auth_db}
                  onChange={(e) => set({ auth_db: e.target.value })}
                  className="bg-surface font-mono"
                />
              </Field>
            )}
          </>
        )}
      </div>

      {!files && hasDatabaseList(form.source_type) && (
        <DatabasePicker
          form={form}
          jobId={jobId}
          error={firstError(errs, "databases")}
          onChange={(patch) => set(patch)}
        />
      )}
      {!files && !hasDatabaseList(form.source_type) && (
        <p className="mt-5 text-[13px] text-ink-3">Redis 整个实例一起导出，不需要选库。</p>
      )}
      {files && <FilesFields form={form} set={set} errs={errs} jobId={jobId} />}
    </Card>
  );
}

function FilesFields({ form, set, errs, jobId }: SectionProps & { jobId: number }) {
  const [draft, setDraft] = React.useState("");
  // 打开选择器时停在哪一行；null 表示没打开
  const [browsing, setBrowsing] = React.useState<number | null>(null);
  // 浏览确认过的路径 → 那次看到的子目录数。按路径记而不是按下标：
  // 增删上面的行会让下标错位，把「已确认可读」挂到另一条没浏览过的路径上。
  const [confirmed, setConfirmed] = React.useState<Record<string, number>>({});
  const pathsErr = firstError(errs, "paths");
  const exErr = firstError(errs, "excludes");
  const addExclude = () => {
    const v = draft.trim();
    if (v && !form.excludes.includes(v)) set({ excludes: [...form.excludes, v] });
    setDraft("");
  };
  return (
    <div className="mt-6 grid gap-5 md:grid-cols-2">
      <div>
        <div className="mb-2 text-[13px] font-medium">目录</div>
        <ul className="flex flex-col gap-2">
          {form.paths.map((p, i) => (
            <li key={i} className="flex gap-1">
              <Input
                value={p}
                placeholder="/path/on/server"
                spellCheck={false}
                aria-label={`目录 ${i + 1}`}
                aria-describedby={pathsErr ? "job-paths-msg" : undefined}
                aria-invalid={pathsErr ? true : undefined}
                onChange={(e) => {
                  const next = e.target.value;
                  set({ paths: form.paths.map((x, j) => (j === i ? next : x)) });
                  if (confirmed[p] !== undefined) {
                    setConfirmed((c) => {
                      const rest = { ...c };
                      delete rest[p];
                      return rest;
                    });
                  }
                }}
                className="bg-surface font-mono text-[13px]"
              />
              <Button
                type="button"
                variant="outline"
                size="icon"
                aria-label={`浏览目录 ${i + 1}`}
                title={form.server_id > 0 ? "浏览服务器上的目录" : "先选服务器"}
                disabled={form.server_id <= 0}
                onClick={() => setBrowsing(i)}
              >
                <FolderOpen />
              </Button>
              {form.paths.length > 1 && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={`移除目录 ${i + 1}`}
                  onClick={() => set({ paths: form.paths.filter((_, j) => j !== i) })}
                >
                  <X />
                </Button>
              )}
            </li>
          ))}
        </ul>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="mt-1 -ml-2 text-brand-ink"
          onClick={() => set({ paths: [...form.paths, ""] })}
        >
          <Plus />
          添加目录
        </Button>
        {form.paths.map((p) =>
          confirmed[p] === undefined ? null : (
            <p key={p} className="mt-1 break-all text-[13px] text-ok">
              {p} 已确认可读，{confirmed[p]} 个子目录
            </p>
          )
        )}
        {form.server_id <= 0 && <p className="mt-1 text-[13px] text-ink-3">先选服务器才能浏览目录。</p>}
        <ErrText id="job-paths">{pathsErr}</ErrText>
        <PathPicker
          open={browsing !== null}
          onOpenChange={(o) => !o && setBrowsing(null)}
          form={form}
          jobId={jobId}
          startPath={browsing !== null ? (form.paths[browsing] ?? "") : ""}
          onSelect={(path, count) => {
            if (browsing === null) return;
            const i = browsing;
            set({ paths: form.paths.map((x, j) => (j === i ? path : x)) });
            setConfirmed((c) => ({ ...c, [path]: count }));
            setBrowsing(null);
          }}
        />
      </div>
      <div>
        <div className="mb-2 text-[13px] font-medium">排除</div>
        <div className="flex min-h-9 flex-wrap items-center gap-1.5 rounded-md border border-line-strong bg-surface px-2 py-1.5">
          {form.excludes.map((d) => (
            <span
              key={d}
              className="inline-flex items-center gap-1 rounded bg-sunken py-0.5 pr-1 pl-2 font-mono text-xs"
            >
              {d}
              <button
                type="button"
                aria-label={`移除排除规则 ${d}`}
                className="rounded text-ink-3 hover:text-ink"
                onClick={() => set({ excludes: form.excludes.filter((x) => x !== d) })}
              >
                <X className="size-3" />
              </button>
            </span>
          ))}
          <input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addExclude();
              }
            }}
            onBlur={addExclude}
            className="min-w-20 flex-1 bg-transparent px-1 text-sm outline-none"
            placeholder="回车添加，如 *.tmp"
            aria-label="添加排除规则"
            aria-describedby={exErr ? "job-excludes-msg" : undefined}
          />
        </div>
        <ErrText id="job-excludes">{exErr}</ErrText>
      </div>
    </div>
  );
}

export function RepoCard({ form, set, errs, repos }: SectionProps & { repos: RepoItem[] }) {
  const err = firstError(errs, "repo_id");
  return (
    <Card title="存到">
      <div
        className="grid gap-2 md:grid-cols-3"
        role="radiogroup"
        aria-label="存储位置"
        aria-describedby={err ? "job-repo-msg" : undefined}
      >
        {repos.map((r) => {
          const off = r.status === "not_mounted";
          const on = form.repo_id === r.id;
          return (
            <Choice key={r.id} checked={on} disabled={off} onClick={() => set({ repo_id: r.id })} className="p-3">
              <div className="flex items-center justify-between">
                <span className="text-[13.5px] font-medium">{r.name}</span>
                {on && <Check className="size-4 text-brand" aria-hidden />}
              </div>
              <div className="mt-0.5 text-xs text-ink-3">
                {off ? (
                  <span className="text-fail">未挂载</span>
                ) : (
                  <>
                    {REPO_TYPE_LABEL[r.type]}
                    {r.free_space != null && `，剩余 ${fmtBytes(r.free_space)}`}
                  </>
                )}
              </div>
            </Choice>
          );
        })}
        {repos.length === 0 && <p className="text-[13px] text-ink-3">还没有存储位置，先去添加一个。</p>}
      </div>
      <ErrText id="job-repo">{err}</ErrText>
    </Card>
  );
}

export function ScheduleCard({
  form,
  set,
  errs,
  next,
  estimate,
  now,
}: SectionProps & { next: NextRuns; estimate: number | null; now: Date }) {
  const s = form.schedule;
  const setS = (patch: Partial<typeof s>) => set({ schedule: { ...s, ...patch } });
  const kindErr = firstError(errs, "schedule.kind");
  const timeErr = firstError(errs, "schedule.time");
  const dayErr = firstError(errs, "schedule.weekday");
  const cronErr = firstError(errs, "schedule.cron") ?? (s.kind === "cron" ? next.error : null);
  const retErr = firstError(errs, "retention");
  const firstNext = next.times && next.times.length > 0 ? next.times[0] : null;
  return (
    <Card title="计划">
      <div className="grid grid-cols-[48px_1fr] items-start gap-x-3 gap-y-5">
        <span className="pt-1 text-[13px] text-ink-2">频率</span>
        <div>
          <div className="flex flex-wrap items-center gap-3">
            <Segmented<ScheduleKind>
              label="频率"
              value={s.kind}
              onChange={(kind) => setS({ kind })}
              options={(Object.keys(SCHEDULE_LABEL) as ScheduleKind[]).map((k) => ({
                value: k,
                label: SCHEDULE_LABEL[k],
              }))}
            />
            {s.kind === "weekly" && (
              <Select value={String(s.weekday)} onValueChange={(v) => setS({ weekday: Number(v) })}>
                <SelectTrigger className="h-8 w-24 bg-surface" aria-label="星期" {...errAria("job-weekday", dayErr)}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {WEEKDAYS.map((d, i) => (
                    <SelectItem key={d} value={String(i)}>
                      {d}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            {(s.kind === "daily" || s.kind === "weekly") && (
              <Input
                value={s.time}
                onChange={(e) => setS({ time: e.target.value })}
                placeholder="03:00"
                aria-label="时刻"
                {...errAria("job-time", timeErr)}
                className="h-8 w-20 bg-surface text-center font-mono"
              />
            )}
            {s.kind === "cron" && (
              <Input
                value={s.cron}
                onChange={(e) => setS({ cron: e.target.value })}
                placeholder="15 3 * * 1-5"
                aria-label="cron 表达式"
                spellCheck={false}
                {...errAria("job-cron", cronErr)}
                className="h-8 w-40 bg-surface font-mono"
              />
            )}
            {firstNext != null && <span className="text-[13px] text-ink-3 num">下次 {fmtNext(firstNext, now)}</span>}
          </div>
          <ErrText id="job-kind">{kindErr}</ErrText>
          <ErrText id="job-weekday">{dayErr}</ErrText>
          <ErrText id="job-time">{timeErr}</ErrText>
          <ErrText id="job-cron">{cronErr}</ErrText>
        </div>

        <span className="pt-1 text-[13px] text-ink-2">保留</span>
        <div>
          <Segmented<RetentionPreset>
            label="保留"
            value={form.retention.preset}
            onChange={(preset) => set({ retention: { ...form.retention, preset } })}
            options={(Object.keys(RETENTION_LABEL) as RetentionPreset[]).map((k) => ({
              value: k,
              label: RETENTION_LABEL[k],
            }))}
          />
        </div>
        <div className="col-start-2">
          <RetentionField
            rule={form.retention}
            estimate={estimate}
            onChange={(retention) => set({ retention })}
            describedBy={retErr ? "job-retention-msg" : undefined}
          />
          <ErrText id="job-retention">{retErr}</ErrText>
        </div>
      </div>
    </Card>
  );
}

const TIMEOUTS = [1800, 3600, 7200, 21600, 43200, 86400];

export function AdvancedCard({ form, set, errs }: SectionProps) {
  const [open, setOpen] = React.useState(false);
  const files = isFiles(form.source_type);
  const timeoutErr = firstError(errs, "advanced.timeout_sec");
  const bwErr = firstError(errs, "advanced.bandwidth_kbps");
  const compErr = firstError(errs, "advanced.compression");
  const argsErr = firstError(errs, "advanced.extra_args");
  // 有字段错误时展开，错误才能显示在对应字段下方
  const expanded = open || !!(timeoutErr || bwErr || compErr || argsErr);
  const timeouts = TIMEOUTS.includes(form.timeout_sec)
    ? TIMEOUTS
    : [...TIMEOUTS, form.timeout_sec].sort((a, b) => a - b);
  return (
    <Sheet>
      <button
        type="button"
        onClick={() => setOpen(!expanded)}
        aria-expanded={expanded}
        className="flex w-full items-center justify-between gap-4 px-5 py-4 text-left"
      >
        <span className="text-[15px] font-semibold">高级</span>
        <span className="flex min-w-0 items-center gap-2 text-[13px] text-ink-3">
          {!expanded && <span className="truncate">{advancedSummary(form)}</span>}
          <ChevronDown className={cn("size-4 shrink-0 transition-transform", expanded && "rotate-180")} aria-hidden />
        </span>
      </button>
      {expanded && (
        <div className="grid gap-4 border-t border-line px-5 py-5 sm:grid-cols-3">
          <Field label="超时" htmlFor="job-timeout" error={timeoutErr}>
            <Select value={String(form.timeout_sec)} onValueChange={(v) => set({ timeout_sec: Number(v) })}>
              <SelectTrigger className="w-full bg-surface" {...errAria("job-timeout", timeoutErr)}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {timeouts.map((t) => (
                  <SelectItem key={t} value={String(t)}>
                    {fmtDuration(t)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="限速" htmlFor="job-bandwidth" error={bwErr}>
            <div className="flex items-center gap-2">
              <Input
                value={form.bandwidth_mb}
                onChange={(e) => set({ bandwidth_mb: e.target.value })}
                placeholder="不限"
                inputMode="decimal"
                {...errAria("job-bandwidth", bwErr)}
                className="w-20 bg-surface text-center num"
              />
              <span className="text-[13px] text-ink-2">MB/s</span>
            </div>
          </Field>
          <Field label="压缩" htmlFor="job-compression" error={compErr}>
            <Select value={form.compression} onValueChange={(v) => set({ compression: v as JobCompression })}>
              <SelectTrigger className="w-full bg-surface" {...errAria("job-compression", compErr)}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(Object.keys(COMPRESSION_LABEL) as JobCompression[]).map((k) => (
                  <SelectItem key={k} value={k}>
                    {COMPRESSION_LABEL[k]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label={files ? "rsync 附加参数" : "导出附加参数"} error={argsErr} className="sm:col-span-3">
            <Input
              value={form.extra_args}
              onChange={(e) => set({ extra_args: e.target.value })}
              placeholder={files ? "--checksum" : "--routines --triggers"}
              spellCheck={false}
              className="bg-surface font-mono"
            />
          </Field>
        </div>
      )}
    </Sheet>
  );
}
