import { HardDrive, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";

import { PageHeader } from "@/components/layout/PageHeader";
import { ChangeLocationDialog, DeleteStorageDialog } from "@/components/storage/ConfirmDialogs";
import { FolderPickerDialog } from "@/components/storage/FolderPickerDialog";
import { SetKeyDialog } from "@/components/storage/SetKeyDialog";
import { StorageFormDialog, type StorageDraft } from "@/components/storage/StorageFormDialog";
import { StorageTable } from "@/components/storage/StorageTable";
import { UnlockDialog, type UnlockTarget } from "@/components/storage/UnlockDialog";
import { ViewKeyDialog, type RevealIntent, type RevealRequest } from "@/components/storage/ViewKeyDialog";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { ErrorCode } from "@/lib/auth";
import {
  createStorage,
  listStorages,
  locationOf,
  probeStorage,
  testStorage,
  unlockStorage,
  updateStorage,
  type ProbeResult,
  type Storage,
} from "@/lib/storage";
import { useOidcErrorMessage } from "@/lib/useOidcError";

type State = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; items: Storage[] };

const errorText = (err: unknown) => (err instanceof Error ? err.message : String(err));

/** 编辑时位置变化、等待确认的内容 */
interface PendingMove {
  storage: Storage;
  draft: StorageDraft;
  probe: ProbeResult;
}

export function StoragePage() {
  const { t } = useTranslation();
  const [state, setState] = useState<State>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [form, setForm] = useState<{ editing?: Storage }>();
  const [formKey, setFormKey] = useState(0);
  const [setKeyDraft, setSetKeyDraft] = useState<StorageDraft>();
  const [unlock, setUnlock] = useState<UnlockTarget>();
  const [move, setMove] = useState<PendingMove>();
  const [moving, setMoving] = useState(false);
  const [moveError, setMoveError] = useState<string>();
  const [deleting, setDeleting] = useState<Storage>();
  // 正在测试连接的存储；可以同时测试多行，每行在完成前不能再次提交
  const [testing, setTesting] = useState<ReadonlySet<number>>(new Set());
  const [actionError, setActionError] = useState<string>();
  const [picker, setPicker] = useState<{ start: string; pick: (path: string) => void }>();
  const [reveal, setReveal] = useState<RevealRequest>();
  const [params, setParams] = useSearchParams();
  const oidcError = useOidcErrorMessage(params);
  const revealID = Number(params.get("reveal"));
  const revealIntent: RevealIntent = params.get("intent") === "download" ? "download" : "view";
  const revealTarget = state.status === "ready" ? state.items.find((i) => i.id === revealID) : undefined;
  // OIDC 再次验证后回到这里（?reveal=<id>&intent=view|download）：列表加载后自动继续查看或下载；关闭时清掉参数
  const afterReauth: RevealRequest | undefined = revealTarget
    ? { storage: revealTarget, intent: revealIntent, afterReauth: true }
    : undefined;

  useEffect(() => {
    let cancelled = false;
    listStorages()
      .then((r) => !cancelled && setState({ status: "ready", items: r.items }))
      .catch(
        (err: unknown) =>
          !cancelled && setState({ status: "error", message: err instanceof Error ? err.message : String(err) })
      );
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const reload = () => setAttempt((n) => n + 1);
  const replace = (item: Storage) =>
    setState((s) => (s.status === "ready" ? { ...s, items: s.items.map((i) => (i.id === item.id ? item : i)) } : s));

  const openForm = (editing?: Storage) => {
    setFormKey((n) => n + 1);
    setForm({ editing });
  };

  const createUnlock = (draft: StorageDraft, probe: ProbeResult): UnlockTarget => ({
    location: probe.location,
    createdAt: probe.created_at,
    unlock: async (key) => (await createStorage({ ...draft, key, confirm_saved: false })).snapshots,
  });
  const moveUnlock = (storage: Storage, draft: StorageDraft, probe: ProbeResult): UnlockTarget => ({
    location: probe.location,
    createdAt: probe.created_at,
    unlock: async (key) =>
      (await updateStorage(storage.id, { ...draft, key, confirm_location_change: true })).snapshots,
  });

  // 建库时位置在测试之后已变成 kopia 仓库（服务端没有覆盖）：重新探测，按第二步的规则判断
  const reprobe = async (draft: StorageDraft, id?: number) => {
    const probe = await probeStorage({ id, name: draft.name, location: draft.location });
    if (probe.state === "not_empty") throw new Error(t("storage.probe.notEmpty"));
    return probe.state === "repository" ? probe : undefined;
  };

  // 新建：空位置设置密钥；已有仓库解锁
  const next = (draft: StorageDraft, probe: ProbeResult) => {
    const editing = form?.editing;
    setForm(undefined);
    if (editing) {
      setMoveError(undefined);
      setMove({ storage: editing, draft, probe });
      return;
    }
    if (probe.state === "empty") {
      setSetKeyDraft(draft);
      return;
    }
    setUnlock(createUnlock(draft, probe));
  };

  const createBecameRepository = async (draft: StorageDraft) => {
    const probe = await reprobe(draft);
    if (!probe) return false;
    setSetKeyDraft(undefined);
    setUnlock(createUnlock(draft, probe));
    return true;
  };

  // 编辑且位置变化：确认后空位置直接保存（沿用托管密钥建库），已有仓库先解锁
  const confirmMove = async () => {
    if (!move || moving) return;
    const { storage, draft, probe } = move;
    if (probe.state === "repository") {
      setMove(undefined);
      setUnlock(moveUnlock(storage, draft, probe));
      return;
    }
    setMoving(true);
    setMoveError(undefined);
    try {
      await updateStorage(storage.id, { ...draft, confirm_location_change: true });
      setMove(undefined);
      reload();
    } catch (err) {
      try {
        const again =
          err instanceof ApiError && err.code === ErrorCode.StorageAlreadyRepository
            ? await reprobe(draft, storage.id)
            : undefined;
        if (again) {
          setMove(undefined);
          setUnlock(moveUnlock(storage, draft, again));
          return;
        }
        setMoveError(errorText(err));
      } catch (probeErr) {
        setMoveError(errorText(probeErr));
      }
    } finally {
      setMoving(false);
    }
  };

  const test = async (s: Storage) => {
    setTesting((ids) => new Set(ids).add(s.id));
    setActionError(undefined);
    try {
      replace((await testStorage(s.id)).item);
    } catch (err) {
      setActionError(errorText(err));
    } finally {
      setTesting((ids) => {
        const rest = new Set(ids);
        rest.delete(s.id);
        return rest;
      });
    }
  };

  // 重新解锁：先探测一次以显示仓库的创建时间，取不到时不显示
  const reunlock = async (s: Storage) => {
    let createdAt = 0;
    try {
      const probe = await probeStorage({ id: s.id, name: s.name, location: locationOf(s) });
      if (probe.state === "repository") createdAt = probe.created_at;
    } catch {
      // 探测失败不影响解锁：提交时服务端会再检查并给出原因
    }
    setUnlock({
      location: s.location,
      createdAt,
      unlock: async (key) => (await unlockStorage(s.id, key)).snapshots,
    });
  };

  return (
    <>
      <PageHeader
        title={t("nav.storage")}
        subtitle={t("storage.subtitle")}
        actions={
          <Button onClick={() => openForm()}>
            <Plus />
            {t("storage.create")}
          </Button>
        }
      />
      <section className="flex flex-col gap-4 px-8 py-6" aria-live="polite">
        {(actionError ?? oidcError) && (
          <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
            {actionError ?? oidcError}
          </p>
        )}
        {state.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
        {state.status === "error" && (
          <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
            <p className="text-sm text-destructive">{t("storage.loadFailed", { message: state.message })}</p>
            <Button variant="outline" size="sm" onClick={reload}>
              {t("common.retry")}
            </Button>
          </div>
        )}
        {state.status === "ready" && state.items.length === 0 && (
          <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center">
            <HardDrive className="size-8 text-faint-foreground" />
            <p className="text-md font-medium">{t("storage.empty")}</p>
            <p className="max-w-md text-sm text-muted-foreground">{t("storage.emptyHint")}</p>
            <Button onClick={() => openForm()}>
              <Plus />
              {t("storage.create")}
            </Button>
          </div>
        )}
        {state.status === "ready" && state.items.length > 0 && (
          <StorageTable
            items={state.items}
            testing={testing}
            actions={{
              onTest: (s) => void test(s),
              onEdit: (s) => openForm(s),
              onUnlock: (s) => void reunlock(s),
              onDelete: setDeleting,
              onRevealKey: (storage) => setReveal({ storage, intent: "view" }),
              onDownloadKey: (storage) => setReveal({ storage, intent: "download" }),
            }}
          />
        )}
      </section>

      <StorageFormDialog
        key={formKey}
        open={form !== undefined}
        editing={form?.editing}
        onOpenChange={(open) => !open && setForm(undefined)}
        onNext={next}
        onSaved={(item) => {
          replace(item);
          setForm(undefined);
        }}
        browse={(start, pick) => setPicker({ start, pick })}
      />
      <FolderPickerDialog
        start={picker?.start}
        onPick={(path) => picker?.pick(path)}
        onClose={() => setPicker(undefined)}
      />
      <ViewKeyDialog
        request={reveal ?? afterReauth}
        onClose={() => {
          setReveal(undefined);
          if (params.has("reveal")) setParams({}, { replace: true });
        }}
      />
      <SetKeyDialog
        draft={setKeyDraft}
        onCancel={() => setSetKeyDraft(undefined)}
        onCreated={() => {
          setSetKeyDraft(undefined);
          reload();
        }}
        onBecameRepository={createBecameRepository}
      />
      <UnlockDialog
        target={unlock}
        onClose={() => setUnlock(undefined)}
        onDone={() => {
          setUnlock(undefined);
          reload();
        }}
      />
      <ChangeLocationDialog
        change={move && { from: move.storage.location, to: move.probe.location }}
        busy={moving}
        error={moveError}
        onCancel={() => setMove(undefined)}
        onConfirm={() => void confirmMove()}
      />
      <DeleteStorageDialog
        storage={deleting}
        onCancel={() => setDeleting(undefined)}
        onDeleted={() => {
          setDeleting(undefined);
          reload();
        }}
        downloadKey={(storage) => setReveal({ storage, intent: "download" })}
      />
    </>
  );
}
