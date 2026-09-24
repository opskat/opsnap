import { HardDrive, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { PageHeader } from "@/components/layout/PageHeader";
import { ChangeLocationDialog, DeleteStorageDialog } from "@/components/storage/ConfirmDialogs";
import { SetKeyDialog } from "@/components/storage/SetKeyDialog";
import { StorageFormDialog, type StorageDraft } from "@/components/storage/StorageFormDialog";
import { StorageTable } from "@/components/storage/StorageTable";
import { UnlockDialog, type UnlockTarget } from "@/components/storage/UnlockDialog";
import { Button } from "@/components/ui/button";
import {
  createStorage,
  listStorages,
  testStorage,
  unlockStorage,
  updateStorage,
  type ProbeResult,
  type Storage,
} from "@/lib/storage";

type State = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; items: Storage[] };

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
  const [deleting, setDeleting] = useState<Storage>();
  const [testing, setTesting] = useState<number>();
  const [actionError, setActionError] = useState<string>();

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

  // 新建：空位置设置密钥；已有仓库解锁
  const next = (draft: StorageDraft, probe: ProbeResult) => {
    const editing = form?.editing;
    setForm(undefined);
    if (editing) {
      setMove({ storage: editing, draft, probe });
      return;
    }
    if (probe.state === "empty") {
      setSetKeyDraft(draft);
      return;
    }
    setUnlock({
      location: probe.location,
      createdAt: probe.created_at,
      unlock: async (key) => (await createStorage({ ...draft, key, confirm_saved: false })).snapshots,
    });
  };

  // 编辑且位置变化：确认后空位置直接保存（沿用托管密钥建库），已有仓库先解锁
  const confirmMove = async () => {
    if (!move) return;
    const { storage, draft, probe } = move;
    setMove(undefined);
    if (probe.state === "repository") {
      setUnlock({
        location: probe.location,
        createdAt: probe.created_at,
        unlock: async (key) =>
          (await updateStorage(storage.id, { ...draft, key, confirm_location_change: true })).snapshots,
      });
      return;
    }
    try {
      await updateStorage(storage.id, { ...draft, confirm_location_change: true });
      reload();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    }
  };

  const test = async (s: Storage) => {
    setTesting(s.id);
    setActionError(undefined);
    try {
      replace((await testStorage(s.id)).item);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setTesting(undefined);
    }
  };

  const reunlock = (s: Storage) =>
    setUnlock({
      location: s.location,
      createdAt: 0,
      unlock: async (key) => (await unlockStorage(s.id, key)).snapshots,
    });

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
        {actionError && (
          <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
            {actionError}
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
              onUnlock: reunlock,
              onDelete: setDeleting,
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
      />
      <SetKeyDialog
        draft={setKeyDraft}
        onCancel={() => setSetKeyDraft(undefined)}
        onCreated={() => {
          setSetKeyDraft(undefined);
          reload();
        }}
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
      />
    </>
  );
}
