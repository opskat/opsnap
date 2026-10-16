// 面板外壳：左侧边栏（三组导航、正在备份、账号菜单、深色模式切换），
// 宽度小于 1024px 时侧边栏收进左侧抽屉。
import * as React from "react";
import { NavLink, useLocation, useNavigate } from "react-router";
import {
  Archive,
  Bell,
  ChevronsUpDown,
  HardDrive,
  History,
  KeyRound,
  LayoutDashboard,
  ListChecks,
  LogOut,
  Menu,
  Moon,
  Server,
  Settings,
  Sun,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import { ChangePasswordDialog } from "@/components/change-password-dialog";
import { ApiError, api, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

export interface NavItem {
  to: string;
  label: string;
  icon: React.ElementType;
}

/** 侧边栏导航：三组，路径与 App.tsx 的路由一致 */
export const NAV: { group?: string; items: NavItem[] }[] = [
  {
    items: [
      { to: "/", label: "概览", icon: LayoutDashboard },
      { to: "/jobs", label: "备份任务", icon: ListChecks },
      { to: "/snapshots", label: "快照", icon: Archive },
      { to: "/runs", label: "运行记录", icon: History },
    ],
  },
  {
    group: "资源",
    items: [
      { to: "/servers", label: "服务器", icon: Server },
      { to: "/repos", label: "存储位置", icon: HardDrive },
    ],
  },
  {
    group: "系统",
    items: [
      { to: "/notify", label: "通知", icon: Bell },
      { to: "/settings", label: "设置", icon: Settings },
    ],
  },
];

/** 标志：三层「地层」，最上面一层用主题色，代表最新的快照 */
export function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 28 28" className={cn("size-7", className)} aria-hidden>
      <rect width="28" height="28" rx="7" className="fill-ink" />
      <rect x="6" y="7.5" width="16" height="3" rx="1.5" fill="var(--brand)" />
      <rect x="6" y="12.5" width="12" height="3" rx="1.5" className="fill-paper" opacity=".75" />
      <rect x="6" y="17.5" width="9" height="3" rx="1.5" className="fill-paper" opacity=".45" />
    </svg>
  );
}

export function Wordmark() {
  return (
    <span className="flex items-center gap-2.5">
      <Mark />
      <span className="text-[16px] font-semibold tracking-[-0.02em]">opsnap</span>
    </span>
  );
}

/** 导航旁的数字，例如需要处理的任务数 */
export type NavCounts = Partial<Record<string, number>>;

function SideNav({ counts, onNavigate }: { counts?: NavCounts; onNavigate?: () => void }) {
  const uid = React.useId();
  return (
    <nav className="flex flex-col gap-6" aria-label="主导航">
      {NAV.map((g, gi) => (
        <div
          key={gi}
          className="flex flex-col gap-0.5"
          role={g.group ? "group" : undefined}
          aria-labelledby={g.group ? `${uid}-g${gi}` : undefined}
        >
          {g.group && (
            <div id={`${uid}-g${gi}`} className="px-2.5 pb-1.5 text-xs text-ink-3">
              {g.group}
            </div>
          )}
          {g.items.map((it) => {
            const count = counts?.[it.to];
            return (
              <NavLink
                key={it.to}
                to={it.to}
                end={it.to === "/"}
                onClick={onNavigate}
                className={({ isActive }) =>
                  cn(
                    "group flex h-9 items-center gap-2.5 rounded-lg px-2.5 text-[13.5px] text-ink-2 transition-colors hover:bg-surface/70 hover:text-ink",
                    isActive && "bg-surface font-medium text-ink shadow-xs ring-1 ring-line"
                  )
                }
              >
                {({ isActive }) => (
                  <>
                    <it.icon
                      className={cn(
                        "size-4 text-ink-3 group-hover:text-ink-2",
                        isActive && "text-brand group-hover:text-brand"
                      )}
                      aria-hidden
                    />
                    <span className="flex-1 text-left">{it.label}</span>
                    {count ? (
                      <span className="rounded-full bg-fail-fill px-1.5 text-[11px] leading-[17px] font-semibold text-white num">
                        <span className="sr-only">，需要处理 </span>
                        {count}
                      </span>
                    ) : null}
                  </>
                )}
              </NavLink>
            );
          })}
        </div>
      ))}
    </nav>
  );
}

/** 正在运行的备份，显示在侧边栏底部 */
export interface RunningInfo {
  runId: number;
  jobName: string;
  /** 例如「已处理 3.4 GB，35.9 MB/s」 */
  progress: string;
}

export function NowRunning({ running, onNavigate }: { running?: RunningInfo | null; onNavigate?: () => void }) {
  if (!running) return <p className="px-2.5 text-xs text-ink-3">当前没有正在运行的备份</p>;
  return (
    <NavLink
      to={`/runs/${running.runId}`}
      onClick={onNavigate}
      className="block w-full rounded-lg border border-line bg-surface p-3 text-left hover:border-line-strong"
    >
      <div className="flex items-center gap-2 text-xs text-brand-ink">
        <span className="relative flex size-2">
          <span className="absolute inset-0 rounded-full bg-brand [animation:dot-ring_1.6s_ease-out_infinite]" />
          <span className="relative size-2 rounded-full bg-brand" />
        </span>
        正在备份
      </div>
      <div className="mt-1.5 text-[13.5px] font-medium">{running.jobName}</div>
      <div className="mt-2 h-1 overflow-hidden rounded-full bg-sunken">
        <div className="h-full w-1/3 rounded-full bg-brand [animation:now-pulse_1.6s_ease-in-out_infinite]" />
      </div>
      <div className="mt-1.5 text-[11.5px] text-ink-3 num">{running.progress}</div>
    </NavLink>
  );
}

function Account({ onChangePassword }: { onChangePassword: () => void }) {
  const { me, setMe } = useAuth();
  const { theme, toggleTheme } = useTheme();
  const navigate = useNavigate();
  const name = me?.username ?? me?.key_name ?? "admin";

  async function logout() {
    try {
      await api.auth.logout();
    } catch (err) {
      // 会话已经失效（401）等同于已退出
      if (!(err instanceof ApiError && err.status === 401)) {
        toast.error(`退出登录失败：${errorMessage(err)}`);
        return;
      }
    }
    setMe(null);
    navigate("/login", { replace: true });
  }

  return (
    <div className="flex items-center gap-1">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            className="flex min-w-0 flex-1 items-center gap-2.5 rounded-lg px-2 py-1.5 text-left hover:bg-surface/70"
            aria-label="账号菜单"
          >
            <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-ink text-[11px] font-semibold text-paper uppercase">
              {name.slice(0, 1)}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium">{name}</span>
              <span className="block truncate text-[11px] text-ink-3">{window.location.host}</span>
            </span>
            <ChevronsUpDown className="size-3.5 text-ink-3" aria-hidden />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" side="top" className="w-52">
          <DropdownMenuLabel className="text-xs font-normal text-ink-3">已登录为 {name}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onChangePassword}>
            <KeyRound />
            修改密码
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={logout}>
            <LogOut />
            退出登录
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Button
        variant="ghost"
        size="icon-sm"
        onClick={toggleTheme}
        aria-label={theme === "dark" ? "切换到浅色" : "切换到深色"}
      >
        {theme === "dark" ? <Sun /> : <Moon />}
      </Button>
    </div>
  );
}

export function Shell({
  children,
  counts,
  running,
}: {
  children: React.ReactNode;
  /** 导航旁的数字，键是导航路径，例如 {"/jobs": 3} */
  counts?: NavCounts;
  running?: RunningInfo | null;
}) {
  const [drawer, setDrawer] = React.useState(false);
  const [changePw, setChangePw] = React.useState(false);
  const location = useLocation();

  // 换页后关闭抽屉并回到顶部
  const [prevPath, setPrevPath] = React.useState(location.pathname);
  if (location.pathname !== prevPath) {
    setPrevPath(location.pathname);
    setDrawer(false);
  }
  React.useEffect(() => {
    window.scrollTo?.(0, 0);
  }, [location.pathname]);

  const body = (onNavigate?: () => void) => (
    <>
      <div className="flex-1 overflow-y-auto px-3">
        <SideNav counts={counts} onNavigate={onNavigate} />
      </div>
      <div className="flex flex-col gap-3 px-3">
        <NowRunning running={running} onNavigate={onNavigate} />
        <div className="border-t border-line pt-3">
          <Account
            onChangePassword={() => {
              onNavigate?.();
              setChangePw(true);
            }}
          />
        </div>
      </div>
    </>
  );

  return (
    <div className="flex min-h-svh">
      <aside className="sticky top-0 hidden h-svh w-[240px] shrink-0 flex-col gap-7 border-r border-line bg-sunken/60 py-5 lg:flex">
        <NavLink to="/" className="self-start rounded-md px-5" aria-label="opsnap 概览">
          <Wordmark />
        </NavLink>
        {body()}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-line bg-paper/90 px-4 backdrop-blur-md lg:hidden">
          <Wordmark />
          <Button variant="ghost" size="icon-sm" onClick={() => setDrawer(true)} aria-label="打开导航">
            <Menu />
          </Button>
        </header>
        <Sheet open={drawer} onOpenChange={setDrawer}>
          <SheetContent side="left" className="w-[272px] gap-7 border-r-0 bg-paper py-5 sm:max-w-[272px] lg:hidden">
            <SheetTitle className="sr-only">导航</SheetTitle>
            <SheetDescription className="sr-only">opsnap 页面导航</SheetDescription>
            <div className="px-5">
              <Wordmark />
            </div>
            {body(() => setDrawer(false))}
          </SheetContent>
        </Sheet>
        <main className="mx-auto w-full max-w-[1240px] px-4 pt-10 pb-24 md:px-10">{children}</main>
      </div>
      <ChangePasswordDialog open={changePw} onOpenChange={setChangePw} />
    </div>
  );
}
