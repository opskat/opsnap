import {
  ArrowLeft,
  Check,
  CircleAlert,
  CircleCheck,
  Copy,
  Cpu,
  Database,
  Loader2,
  RefreshCw,
  Server,
  Terminal,
  TriangleAlert,
  Waypoints,
} from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router";

import { DataSourceFormDialog } from "@/components/sources/DataSourceFormDialog";
import { HostKeyDialog } from "@/components/sources/HostKeyDialog";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { relativeTime } from "@/lib/format";
import {
  confirmChannelHostKey,
  confirmDataSourceHostKey,
  dataSourceHostKeyRequest,
  dataSourceVersionLabel,
  getDataSource,
  listChannels,
  pickProbeText,
  reprobeDataSource,
  testDataSource,
  type ChannelHop,
  type ChannelItem,
  type DataSourceItem,
  type DataSourceSaveResult,
  type HostKeyPrompt,
} from "@/lib/sources";
import { cn } from "@/lib/utils";

/** 探测进行中时的轮询间隔：足够快让页面及时反映结果，又不至于给后端太大压力 */
const POLL_INTERVAL_MS = 3000;

type LoadState =
  | { status: "loading" }
  | { status: "not_found" }
  | { status: "error"; message: string }
  | { status: "ready"; item: DataSourceItem };

const statusStyle: Record<DataSourceItem["status"], string> = {
  ok: "bg-success-soft text-success",
  unreachable: "bg-destructive-soft text-destructive",
  host_key_changed: "bg-warning-soft text-warning",
};

const tierStyle: Record<string, string> = {
  ok: "text-success",
  warn: "text-warning",
  fail: "text-destructive",
};

function tierIcon(tier: string) {
  if (tier === "fail") return <CircleAlert className={cn("size-4 shrink-0", tierStyle.fail)} />;
  if (tier === "warn") return <TriangleAlert className={cn("size-4 shrink-0", tierStyle.warn)} />;
  return <CircleCheck className={cn("size-4 shrink-0", tierStyle.ok)} />;
}

/** 链路中一跳的类型标注：通道用 SSH / SOCKS5 缩写（与表单链路预览一致），数据源本身用其类型名称 */
function hopTypeLabel(t: (key: string) => string, kind: string) {
  return kind === "ssh" || kind === "socks5" ? kind.toUpperCase() : t(`sources.dataSource.kind.${kind}`);
}

function hopIcon(kind: string) {
  if (kind === "ssh") return <Terminal className="size-4" />;
  if (kind === "socks5") return <Waypoints className="size-4" />;
  if (kind === "server_file") return <Server className="size-4" />;
  return <Database className="size-4" />;
}

/** 链路中一跳的 SSH 主机密钥指纹：通道跳从通道列表取，数据源自身（服务器文件）从数据源取 */
function hopFingerprint(hop: ChannelHop, isLast: boolean, item: DataSourceItem, channels: ChannelItem[]) {
  if (hop.kind !== "ssh") return undefined;
  if (isLast) {
    if (item.kind !== "server_file") return undefined;
    return item.status === "host_key_changed" && !item.failed_hop?.channel_id ? item.presented_host_key : item.host_key;
  }
  const channel = channels.find((c) => c.id === hop.id);
  if (!channel) return undefined;
  return channel.status === "host_key_changed" ? channel.presented_host_key : channel.host_key;
}

/** 数据源详情页：/sources/:id */
export function SourceDetailPage() {
  const { t, i18n } = useTranslation();
  const params = useParams<{ id: string }>();
  const numericId = Number(params.id);
  const validId = Number.isInteger(numericId) && numericId > 0;

  const [state, setState] = useState<LoadState>(() => (validId ? { status: "loading" } : { status: "not_found" }));
  const [attempt, setAttempt] = useState(0);
  const [channels, setChannels] = useState<ChannelItem[]>([]);
  const [testing, setTesting] = useState(false);
  const [reprobing, setReprobing] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const [copiedKey, setCopiedKey] = useState<string>();
  const [formOpen, setFormOpen] = useState(false);
  const [formKey, setFormKey] = useState(0);
  const [rowHostKey, setRowHostKey] = useState<{
    prompt: HostKeyPrompt;
    confirm: (fingerprint: string) => Promise<DataSourceSaveResult>;
  }>();
  const [hostKeyBusy, setHostKeyBusy] = useState(false);
  const [hostKeyError, setHostKeyError] = useState<string>();

  useEffect(() => {
    if (!validId) return;
    let cancelled = false;
    getDataSource(numericId)
      .then((r) => !cancelled && setState({ status: "ready", item: r.item }))
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 404) setState({ status: "not_found" });
        else setState({ status: "error", message: err instanceof Error ? err.message : String(err) });
      });
    listChannels()
      .then((r) => !cancelled && setChannels(r.items))
      .catch(() => {
        // 通道列表仅用于链路指纹展示与经由通道的重新确认，加载失败不影响详情页其余内容
      });
    return () => {
      cancelled = true;
    };
  }, [numericId, validId, attempt]);

  const probeState = state.status === "ready" ? (state.item.probe?.state ?? null) : null;

  // 探测进行中时定时轮询，直到结果落定（done / unprobeable）；组件卸载或探测结束时停止
  useEffect(() => {
    if (probeState !== "probing") return;
    let cancelled = false;
    let timer: number | undefined;
    const poll = () => {
      timer = window.setTimeout(() => {
        if (cancelled) return;
        getDataSource(numericId)
          .then((r) => {
            if (cancelled) return;
            setState({ status: "ready", item: r.item });
            if (r.item.probe?.state === "probing") poll();
          })
          .catch(() => {
            if (!cancelled) poll();
          });
      }, POLL_INTERVAL_MS);
    };
    poll();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [probeState, numericId]);

  const openHostKeyPrompt = (
    prompt: HostKeyPrompt,
    confirm: (fingerprint: string) => Promise<DataSourceSaveResult>
  ) => {
    setHostKeyError(undefined);
    setRowHostKey({ prompt, confirm });
  };

  /** 数据源自身或经由通道确认新主机密钥；确认经由通道的密钥后刷新通道与详情，反映最新状态 */
  const confirmViaChannelOrSelf = (item: DataSourceItem, channelId: number | undefined) => {
    if (channelId) {
      return async (fingerprint: string): Promise<DataSourceSaveResult> => {
        const res = await confirmChannelHostKey(channelId, fingerprint);
        if (!res.host_key) {
          if (res.item) {
            const updated = res.item;
            setChannels((cs) => cs.map((c) => (c.id === channelId ? updated : c)));
          }
          const fresh = await getDataSource(numericId);
          setState({ status: "ready", item: fresh.item });
        }
        return { item: null, host_key: res.host_key };
      };
    }
    return (fingerprint: string) => confirmDataSourceHostKey(item.id, fingerprint);
  };

  const test = async () => {
    if (state.status !== "ready") return;
    setTesting(true);
    setActionError(undefined);
    try {
      const res = await testDataSource(state.item.id);
      setState({ status: "ready", item: res.item });
      if (res.host_key) {
        openHostKeyPrompt(res.host_key, confirmViaChannelOrSelf(res.item, res.item.failed_hop?.channel_id));
      }
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setTesting(false);
    }
  };

  const reprobe = async () => {
    if (state.status !== "ready") return;
    setReprobing(true);
    setActionError(undefined);
    try {
      const res = await reprobeDataSource(state.item.id);
      setState({ status: "ready", item: res.item });
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setReprobing(false);
    }
  };

  const reconfirm = (item: DataSourceItem) => {
    const { prompt, viaChannel } = dataSourceHostKeyRequest(item, channels);
    openHostKeyPrompt(prompt, confirmViaChannelOrSelf(item, viaChannel?.id));
  };

  const openEdit = () => {
    setFormKey((n) => n + 1);
    setFormOpen(true);
  };

  const trustHostKey = async () => {
    if (!rowHostKey) return;
    setHostKeyBusy(true);
    setHostKeyError(undefined);
    try {
      const res = await rowHostKey.confirm(rowHostKey.prompt.fingerprint);
      if (res.host_key) {
        setRowHostKey({ ...rowHostKey, prompt: res.host_key });
        return;
      }
      if (res.item) setState({ status: "ready", item: res.item });
      setRowHostKey(undefined);
    } catch (err) {
      setHostKeyError(err instanceof Error ? err.message : String(err));
    } finally {
      setHostKeyBusy(false);
    }
  };

  const copyFix = async (key: string, text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedKey(key);
      window.setTimeout(() => setCopiedKey((k) => (k === key ? undefined : k)), 1500);
    } catch {
      // 剪贴板不可用时静默失败，不影响页面其余功能
    }
  };

  if (state.status === "loading") {
    return (
      <section className="px-8 py-6">
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      </section>
    );
  }

  if (state.status === "not_found") {
    return (
      <section className="flex flex-col items-center gap-3 px-8 py-16 text-center">
        <p className="text-lg font-medium">{t("sources.dataSource.detail.notFoundTitle")}</p>
        <p className="text-sm text-muted-foreground">{t("sources.dataSource.detail.notFoundHint")}</p>
        <Button asChild variant="outline">
          <Link to="/sources">{t("sources.dataSource.detail.backToList")}</Link>
        </Button>
      </section>
    );
  }

  if (state.status === "error") {
    return (
      <section className="px-8 py-6">
        <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
          <p className="text-sm text-destructive">
            {t("sources.dataSource.detail.loadFailed", { message: state.message })}
          </p>
          <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>
            {t("common.retry")}
          </Button>
        </div>
      </section>
    );
  }

  const item = state.item;
  const probe = item.probe;
  const lang = i18n.language;
  const pick = (text: { zh_cn: string; en: string }) => pickProbeText(text, lang);
  const hops = [
    {
      key: "opsnap",
      kind: "opsnap",
      name: "OpsNap",
      subtitle: t("sources.dataSource.detail.connection.controller"),
      fingerprint: undefined as string | undefined,
    },
    ...item.chain.map((hop, i) => ({
      key: `${hop.kind}-${hop.id}-${i}`,
      kind: hop.kind,
      name: hop.name,
      subtitle: [hopTypeLabel(t, hop.kind), hop.address].join(" · "),
      fingerprint: hopFingerprint(hop, i === item.chain.length - 1, item, channels),
    })),
  ];
  const tlsInfo =
    item.kind !== "server_file"
      ? [
          t(`sources.dataSource.form.tlsMode.${item.tls_mode}`),
          item.server?.tls?.version,
          item.tls_client_cert ? t("sources.dataSource.detail.connection.clientCert") : undefined,
        ]
          .filter(Boolean)
          .join(" · ")
      : undefined;
  const recentTestText =
    item.status === "ok"
      ? t("sources.dataSource.list.checked", { time: relativeTime(t, item.checked_at) })
      : item.status_message || t("sources.dataSource.detail.connection.untested");

  return (
    <>
      <header className="flex flex-col gap-3 border-b px-8 py-5">
        <Link
          to="/sources"
          className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-4" />
          {t("sources.dataSource.detail.back", { name: t("nav.sources") })}
        </Link>
        <div className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-1">
            <div className="flex items-center gap-2">
              <h1 className="text-2xl font-bold">{item.name}</h1>
              <span className={cn("rounded-sm px-1.75 py-0.5 text-2xs font-medium", statusStyle[item.status])}>
                {t(`sources.dataSource.status.${item.status}`)}
              </span>
            </div>
            <p className="font-mono text-sm text-muted-foreground">
              {[dataSourceVersionLabel(item), item.address, item.checked_at ? recentTestText : undefined]
                .filter(Boolean)
                .join(" · ")}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" disabled={testing} onClick={() => void test()}>
              {testing ? t("sources.dataSource.form.testing") : t("sources.dataSource.list.test")}
            </Button>
            <Button variant="outline" onClick={openEdit}>
              {t("sources.dataSource.list.edit")}
            </Button>
            <Button disabled={reprobing || probe?.state === "probing"} onClick={() => void reprobe()}>
              <RefreshCw className={cn("size-4", reprobing && "animate-spin")} />
              {reprobing ? t("sources.dataSource.detail.reprobing") : t("sources.dataSource.detail.reprobe")}
            </Button>
          </div>
        </div>
        {actionError && (
          <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
            {actionError}
          </p>
        )}
      </header>

      <section className="grid grid-cols-1 items-start gap-6 px-8 py-6 lg:grid-cols-[minmax(280px,360px)_1fr]">
        <div
          role="region"
          aria-label={t("sources.dataSource.detail.connection.title")}
          className="rounded-lg border bg-card"
        >
          <h2 className="border-b px-4 py-3 text-sm font-medium">{t("sources.dataSource.detail.connection.title")}</h2>
          <dl className="flex flex-col gap-3 px-4 py-3 text-sm">
            <DetailRow label={t("sources.dataSource.detail.connection.type")}>
              {t(`sources.dataSource.kind.${item.kind}`)}
            </DetailRow>
            <DetailRow label={t("sources.dataSource.detail.connection.address")}>
              <span className="font-mono">{item.address}</span>
            </DetailRow>
            <DetailRow label={t("sources.dataSource.detail.connection.username")}>{item.username}</DetailRow>
            {tlsInfo && <DetailRow label={t("sources.dataSource.detail.connection.tls")}>{tlsInfo}</DetailRow>}
            <DetailRow label={t("sources.dataSource.detail.connection.recentTest")}>{recentTestText}</DetailRow>
          </dl>
          <h3 className="border-t px-4 py-3 text-sm font-medium">{t("sources.dataSource.detail.connection.chain")}</h3>
          <ol aria-label={t("sources.dataSource.detail.connection.chain")} className="flex flex-col gap-3 px-4 pb-4">
            {hops.map((hop) => (
              <li key={hop.key} className="flex items-start gap-2.5">
                <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground">
                  {hop.kind === "opsnap" ? <Cpu className="size-4" /> : hopIcon(hop.kind)}
                </span>
                <span className="flex min-w-0 flex-col">
                  <span className="truncate font-medium">{hop.name}</span>
                  <span className="truncate font-mono text-xs text-muted-foreground">
                    {hop.subtitle}
                    {hop.fingerprint ? ` · ${hop.fingerprint}` : ""}
                  </span>
                </span>
              </li>
            ))}
          </ol>
        </div>

        <div
          role="region"
          aria-label={t("sources.dataSource.detail.probe.title")}
          className="rounded-lg border bg-card"
        >
          <h2 className="flex items-center justify-between border-b px-4 py-3 text-sm font-medium">
            <span>{t("sources.dataSource.detail.probe.title")}</span>
            <ProbeSummary probe={probe} />
          </h2>
          {(!probe || probe.state === "probing") && (
            <p className="flex items-center gap-2 px-4 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 shrink-0 animate-spin" />
              {t("sources.dataSource.list.probing")}
            </p>
          )}
          {probe?.state === "unprobeable" && (
            <p className="flex items-center gap-2 px-4 py-6 text-sm text-muted-foreground">
              <span>{t("sources.dataSource.list.unprobeable")}</span>
              {probe.error && <span className="text-destructive">{probe.error}</span>}
            </p>
          )}
          {probe?.state === "done" && (
            <ol className="flex flex-col divide-y">
              {(probe.items ?? []).map((probeItem) => (
                <li key={probeItem.key} className="flex flex-col gap-1.5 px-4 py-3">
                  <span className="flex items-center gap-2 text-sm font-medium">
                    {tierIcon(probeItem.tier)}
                    {pick(probeItem.title)}
                  </span>
                  <span className="pl-6 text-xs text-muted-foreground">
                    {pick(probeItem.detail)}
                    {probeItem.tables && probeItem.tables.length > 0 && (
                      <>
                        {" "}
                        {t("sources.dataSource.detail.probe.tables", {
                          tables: probeItem.tables.join("、"),
                          count: probeItem.table_count ?? probeItem.tables.length,
                        })}
                      </>
                    )}
                  </span>
                  {probeItem.tier !== "ok" && pick(probeItem.fix) && (
                    <div className="ml-6 flex items-center justify-between gap-3 rounded-md bg-warning-soft px-3 py-2 text-xs text-warning">
                      <code className="whitespace-pre-wrap break-all">{pick(probeItem.fix)}</code>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="shrink-0"
                        onClick={() => void copyFix(probeItem.key, pick(probeItem.fix))}
                      >
                        {copiedKey === probeItem.key ? <Check /> : <Copy />}
                        {copiedKey === probeItem.key ? t("common.copied") : t("common.copy")}
                      </Button>
                    </div>
                  )}
                </li>
              ))}
            </ol>
          )}
        </div>
      </section>

      <DataSourceFormDialog
        key={formKey}
        open={formOpen}
        editing={item}
        channels={channels}
        onOpenChange={setFormOpen}
        onSaved={(saved) => {
          setState({ status: "ready", item: saved });
          setFormOpen(false);
        }}
        onReconfirm={reconfirm}
      />
      <HostKeyDialog
        request={rowHostKey}
        busy={hostKeyBusy}
        error={hostKeyError}
        onCancel={() => setRowHostKey(undefined)}
        onTrust={() => void trustHostKey()}
      />
    </>
  );
}

function ProbeSummary({ probe }: { probe: DataSourceItem["probe"] }) {
  const { t } = useTranslation();
  if (probe?.state === "unprobeable" && probe.time) {
    // 无法探测也是一次探测结果，同样带探测时间
    return (
      <span className="text-xs font-normal text-muted-foreground">
        {t("sources.dataSource.detail.probe.probedAt", { time: relativeTime(t, probe.time) })}
      </span>
    );
  }
  if (!probe || probe.state !== "done") return null;
  const parts: string[] = [];
  if (probe.fail === 0 && probe.warn === 0) {
    parts.push(t("sources.dataSource.list.probeAllOk", { count: probe.ok }));
  } else {
    if (probe.ok > 0) parts.push(t("sources.dataSource.list.probeOk", { count: probe.ok }));
    if (probe.warn > 0) parts.push(t("sources.dataSource.list.probeWarn", { count: probe.warn }));
    if (probe.fail > 0) parts.push(t("sources.dataSource.list.probeFail", { count: probe.fail }));
  }
  parts.push(t("sources.dataSource.detail.probe.probedAt", { time: relativeTime(t, probe.time) }));
  return <span className="text-xs font-normal text-muted-foreground">{parts.join(" · ")}</span>;
}

function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="truncate text-right">{children}</dd>
    </div>
  );
}
