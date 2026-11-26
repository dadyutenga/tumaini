import { useEffect, useState } from "react";
import { Bell, KeyRound, Plus, Search, SunMoon } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { useVisibleRoutes } from "@/app/useVisibleRoutes";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";
import { useAuth } from "@/features/auth/auth";
import { useI18n } from "@/features/i18n/i18n";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
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

interface SearchHit {
  kind: string;
  id: string;
  title: string;
  subtitle: string;
  link: string;
}

const KIND_LABELS: Record<string, string> = {
  product: "Product",
  customer: "Shop customer",
  client: "Service client",
  project: "Project",
  design_job: "Design job",
  sale: "Sale / receipt",
  service_invoice: "Service invoice",
};

export function SearchPalette() {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const debounced = useDebounce(query.trim(), 220);
  const navigate = useNavigate();
  const routes = useVisibleRoutes();
  const hits = useGet<SearchHit[]>(
    "/search",
    { q: debounced },
    { enabled: open && debounced.length >= 2 },
  );

  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen(true);
      }
    };
    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, []);

  const matchedRoutes = routes
    .filter((route) => route.navigation)
    .filter((route) => !query.trim() || route.label.toLowerCase().includes(query.toLowerCase()));

  const go = (to: string) => {
    setOpen(false);
    setQuery("");
    navigate(to);
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="h-8 gap-2 border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] px-2.5 text-[var(--od-ink-muted)] shadow-none hover:text-[var(--od-ink)]"
          aria-label="Global search"
        >
          <Search size={14} />
          <span className="hidden text-xs lg:inline">{t.actions.globalSearchPlaceholder}</span>
          <kbd className="od-mono hidden rounded border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--od-ink-muted)] xl:inline">
            Ctrl K
          </kbd>
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl">
        <DialogTitle>Global search</DialogTitle>
        <DialogDescription>
          Find clients, customers, products, invoices, projects, receipts or pages.
        </DialogDescription>
        <Input
          autoFocus
          aria-label="Global search input"
          placeholder="Type a name, phone, number, SKU or barcode…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <div className="scrollbar-thin max-h-80 space-y-3 overflow-auto">
          {debounced.length >= 2 && (
            <div>
              <p className="mb-1.5 px-2 text-[11px] font-semibold uppercase tracking-[0.04em] text-[var(--od-ink-muted)]">
                Records {hits.isFetching ? "…" : `(${hits.data?.length ?? 0})`}
              </p>
              {(hits.data ?? []).length === 0 && !hits.isFetching ? (
                <p className="px-2 py-2 text-[13px] text-[var(--od-ink-muted)]">
                  No matching records.
                </p>
              ) : (
                <ul className="space-y-0.5">
                  {(hits.data ?? []).map((h) => (
                    <li key={`${h.kind}-${h.id}`}>
                      <button
                        type="button"
                        className="flex w-full items-center justify-between gap-2 rounded-[var(--od-radius-sm,8px)] px-2.5 py-2 text-left text-[13px] transition-colors hover:bg-[var(--od-surface-subtle)] focus-visible:bg-[var(--od-surface-subtle)]"
                        onClick={() => go(h.link)}
                      >
                        <span className="min-w-0 truncate">
                          <span className="font-medium text-[var(--od-ink)]">{h.title}</span>
                          {h.subtitle && (
                            <span className="ml-2 text-xs text-[var(--od-ink-muted)]">
                              {h.subtitle}
                            </span>
                          )}
                        </span>
                        <span className="shrink-0 rounded-full bg-[var(--od-neutral-soft)] px-2 py-0.5 text-[11px] font-medium text-[var(--od-ink-secondary)]">
                          {KIND_LABELS[h.kind] ?? h.kind}
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
          {matchedRoutes.length > 0 && (
            <div>
              <p className="mb-1.5 px-2 text-[11px] font-semibold uppercase tracking-[0.04em] text-[var(--od-ink-muted)]">
                Pages
              </p>
              <ul className="space-y-0.5">
                {matchedRoutes.slice(0, 8).map((route) => (
                  <li key={route.path}>
                    <button
                      type="button"
                      className="w-full rounded-[var(--od-radius-sm,8px)] px-2.5 py-2 text-left text-[13px] text-[var(--od-ink)] transition-colors hover:bg-[var(--od-surface-subtle)] focus-visible:bg-[var(--od-surface-subtle)]"
                      onClick={() => go(route.path)}
                    >
                      {route.label}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function QuickNewMenu({ floating = false }: { floating?: boolean }) {
  const { can, canAny } = useAuth();
  const navigate = useNavigate();
  const actions = [
    { label: "New sale (POS)", to: "/pos", show: can("shop.sale.create") },
    { label: "New client", to: "/clients", show: can("services.client.manage") },
    { label: "New project", to: "/projects", show: can("services.project.manage") },
    { label: "New design job", to: "/design-jobs", show: can("services.design.manage") },
    {
      label: "Receive payment",
      to: "/service-invoices",
      show: canAny("services.invoice.manage", "finance.payment.receive"),
    },
    {
      label: "Customer payment",
      to: "/customers",
      show: canAny("shop.customer.view_all", "shop.customer.manage", "finance.payment.receive"),
    },
    {
      label: "New expense",
      to: "/expenses",
      show: canAny("finance.expense.submit", "finance.expense.enter"),
    },
    { label: "New task", to: "/service-tasks", show: can("services.task.manage") },
  ].filter((a) => a.show);

  if (actions.length === 0) return null;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {floating ? (
          <Button
            size="lg"
            className="fixed bottom-5 right-5 z-30 h-11 rounded-full px-4 shadow-[var(--od-shadow-pop)] md:hidden"
            aria-label="Quick create menu"
          >
            <Plus size={16} /> New
          </Button>
        ) : (
          <Button size="sm" className="hidden h-8 sm:inline-flex" aria-label="Quick create menu">
            <Plus size={14} /> New
          </Button>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel className="text-xs text-[var(--od-ink-muted)]">
          Quick actions
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
          aria-label={`Notifications, ${unread} unread`}
          className="relative"
        >
          <Bell size={17} />
          {unread > 0 && (
            <span className="od-tabular absolute right-0.5 top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-[var(--od-bad-mark)] px-1 text-[10px] font-semibold text-white">
              {unread > 99 ? "99+" : unread}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 rounded-[var(--od-radius,16px)] p-4">
        <div className="flex items-center justify-between">
          <h2 className="od-card-title">Notifications</h2>
          <Link
            to="/notifications"
            onClick={() => setOpen(false)}
            className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
          >
            View all
          </Link>
        </div>
        {items.length === 0 ? (
          <p className="mt-3 text-[13px] text-[var(--od-ink-muted)]">You are all caught up.</p>
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
            Mark all read
          </Button>
        )}
      </PopoverContent>
    </Popover>
  );
}

export function ThemeToggle() {
  const [theme, setTheme] = useState(() => localStorage.getItem("theme") ?? "system");
  useEffect(() => {
    const update = () => setTheme(localStorage.getItem("theme") ?? "system");
    window.addEventListener("themechange", update);
    return () => window.removeEventListener("themechange", update);
  }, []);
  const toggle = () => {
    const next = theme === "system" ? "light" : theme === "light" ? "dark" : "system";
    localStorage.setItem("theme", next);
    setTheme(next);
    window.dispatchEvent(new Event("themechange"));
  };
  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={toggle}
      title={`Theme: ${theme}`}
      aria-label={`Theme: ${theme}. Change theme`}
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
        <Button variant="ghost" aria-label="Open user menu" className="h-8 gap-2 px-1.5">
          <span className="grid h-6 w-6 place-items-center rounded-full bg-[var(--od-neutral-soft)] text-[11px] font-semibold text-[var(--od-ink)] ring-1 ring-[var(--od-border-strong)]">
            {initials}
          </span>
          <span className="hidden text-left sm:inline">
            <span className="block text-[12px] font-medium leading-tight text-[var(--od-ink)]">
              {user?.name}
            </span>
            <span className="block text-[10px] leading-tight text-[var(--od-ink-muted)]">
              {me?.is_owner ? "Owner" : user?.role_name}
            </span>
          </span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>
          <p className="truncate text-[13px] font-semibold">{user?.name}</p>
          <p className="text-[11px] font-normal text-[var(--od-ink-muted)]">
            {me?.is_owner ? "Owner / Admin" : user?.role_name}
          </p>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/profile">{t.nav.profile}</Link>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link to="/profile#password">
            <KeyRound size={14} className="mr-1.5" /> Change password
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void logout()}>{t.actions.signOut}</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
