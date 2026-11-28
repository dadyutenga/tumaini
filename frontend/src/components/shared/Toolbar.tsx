import { useEffect, useState } from "react";
import { Bell, KeyRound, Plus, SunMoon } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";
import { useAuth } from "@/features/auth/auth";
import { useI18n } from "@/features/i18n/i18n";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { fmt } from "@/features/i18n/format";
import { useQuickActions } from "@/features/search/searchActions";

export function QuickNewMenu({ floating = false }: { floating?: boolean }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const actions = useQuickActions().create;

  if (actions.length === 0) return null;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {floating ? (
          <Button
            size="lg"
            className="fixed bottom-5 right-5 z-30 h-11 rounded-full px-4 shadow-[var(--od-shadow-pop)] md:hidden"
            aria-label={t.common.quickCreateMenu}
          >
            <Plus size={16} /> {t.common.newQuick}
          </Button>
        ) : (
          <Button
            size="sm"
            className="hidden h-8 sm:inline-flex"
            aria-label={t.common.quickCreateMenu}
          >
            <Plus size={14} /> {t.common.newQuick}
          </Button>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel className="text-xs text-[var(--od-ink-muted)]">
          {t.common.quickActions}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {actions.map((a) => (
          <DropdownMenuItem key={a.label} onSelect={() => navigate(a.to)}>
            {a.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

interface Notice {
  id: string;
  title?: string;
  body?: string;
  message?: string;
  link?: string | null;
  is_read?: boolean;
  read_at?: string | null;
  created_at?: string;
}

export function NotificationBell() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const { data } = useGet<Notice[]>("/notifications");
  const markRead = useWrite<{ id: string }>("POST", (b) => `/notifications/${b.id}/read`, {
    invalidate: ["/notifications"],
    toBody: () => undefined,
  });
  const readAll = useWrite("POST", "/notifications/read-all", { invalidate: ["/notifications"] });
  const items = data ?? [];
  const unread = items.filter((n) => !(n.is_read ?? Boolean(n.read_at))).length;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={fmt(t.common.notificationsUnread, { unread })}
          className="relative"
        >
          <Bell size={17} />
          {unread > 0 && (
            <span className="od-tabular absolute right-0.5 top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-[var(--od-bad-pill)] px-1 text-[10px] font-semibold text-[var(--od-badge-on-solid)]">
              {unread > 99 ? "99+" : unread}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 rounded-[var(--od-radius,16px)] p-4">
        <div className="flex items-center justify-between">
          <h2 className="od-card-title">{t.common.notifications}</h2>
          <Link
            to="/notifications"
            onClick={() => setOpen(false)}
            className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
          >
            {t.common.viewAll}
          </Link>
        </div>
        {items.length === 0 ? (
          <p className="mt-3 text-[13px] text-[var(--od-ink-muted)]">
            {t.common.youAreAllCaughtUp}
          </p>
        ) : (
          <ul className="scrollbar-thin mt-3 max-h-72 space-y-1.5 overflow-auto text-[13px]">
            {items.slice(0, 15).map((n) => {
              const read = n.is_read ?? Boolean(n.read_at);
              return (
                <li key={n.id}>
                  <button
                    type="button"
                    onClick={() => {
                      if (!read) markRead.mutate({ id: n.id });
                      setOpen(false);
                      if (n.link) navigate(n.link);
                    }}
                    className={cn(
                      "w-full rounded-[var(--od-radius-sm,8px)] border border-transparent p-2.5 text-left transition-colors hover:bg-[var(--od-surface-subtle)]",
                      !read && "border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)]",
                    )}
                  >
                    <div className="flex items-center gap-2">
                      {!read && (
                        <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-[var(--od-info)]" />
                      )}
                      <p className={cn("truncate", read ? "font-normal" : "font-semibold")}>
                        {n.title ?? n.message}
                      </p>
                    </div>
                    {n.body && (
                      <p className="mt-0.5 line-clamp-2 text-xs text-[var(--od-ink-secondary)]">
                        {n.body}
                      </p>
                    )}
                    <p className="mt-1 text-[11px] text-[var(--od-ink-muted)]">
                      {formatDateTime(n.created_at)}
                    </p>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {unread > 0 && (
          <Button
            variant="outline"
            size="sm"
            className="mt-3 w-full"
            onClick={() => readAll.mutate(undefined)}
          >
            {t.common.markAllRead}
          </Button>
        )}
      </PopoverContent>
    </Popover>
  );
}

const THEME_ORDER = ["system", "light", "high-contrast", "dark"] as const;
export function ThemeToggle() {
  const { t } = useI18n();
  const [theme, setTheme] = useState(() => localStorage.getItem("theme") ?? "system");
  useEffect(() => {
    const update = () => setTheme(localStorage.getItem("theme") ?? "system");
    window.addEventListener("themechange", update);
    return () => window.removeEventListener("themechange", update);
  }, []);
  const toggle = () => {
    const idx = THEME_ORDER.indexOf(theme as (typeof THEME_ORDER)[number]);
    const next = THEME_ORDER[(idx + 1) % THEME_ORDER.length];
    localStorage.setItem("theme", next);
    setTheme(next);
    window.dispatchEvent(new Event("themechange"));
  };
  const themeLabels: Record<string, string> = {
    system: t.common.system,
    light: t.common.softLight,
    "high-contrast": t.common.highContrastLight,
    dark: t.common.dark,
  };
  const label = themeLabels[theme] ?? theme;
  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={toggle}
      title={fmt(t.common.theme, { label })}
      aria-label={fmt(t.common.themeChangeTheme, { label })}
    >
      <SunMoon size={17} />
    </Button>
  );
}

export function UserMenu() {
  const { user, me, logout } = useAuth();
  const { t } = useI18n();
  const initials =
    (user?.name ?? "U")
      .split(/\s+/)
      .slice(0, 2)
      .map((w) => w[0]?.toUpperCase() ?? "")
      .join("") || "U";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" aria-label={t.common.openUserMenu} className="h-8 gap-2 px-1.5">
          <span className="grid h-6 w-6 place-items-center rounded-full bg-[var(--od-neutral-soft)] text-[11px] font-semibold text-[var(--od-ink)] ring-1 ring-[var(--od-border-strong)]">
            {initials}
          </span>
          <span className="hidden text-left sm:inline">
            <span className="block text-[12px] font-medium leading-tight text-[var(--od-ink)]">
              {user?.name}
            </span>
            <span className="block text-[10px] leading-tight text-[var(--od-ink-muted)]">
              {me?.is_owner ? t.common.owner : user?.role_name}
            </span>
          </span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>
          <p className="truncate text-[13px] font-semibold">{user?.name}</p>
          <p className="text-[11px] font-normal text-[var(--od-ink-muted)]">
            {me?.is_owner ? t.common.ownerAdmin : user?.role_name}
          </p>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/profile">{t.nav.profile}</Link>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link to="/profile#password">
            <KeyRound size={14} className="mr-1.5" /> {t.common.changePassword}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void logout()}>{t.common.signOut}</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
