import { APP_NAME } from "@/lib/constants";
import { useMemo, useState } from "react";
import { Link, NavLink, Outlet, matchPath, useLocation } from "react-router-dom";
import { ChevronDown, ChevronRight, Menu, PanelLeft, PanelLeftClose } from "lucide-react";
import { routes, type AppRoute } from "@/app/routes";
import { useVisibleRoutes } from "@/app/useVisibleRoutes";
import { BranchSwitcher } from "@/components/layout/BranchSwitcher";
import { NotificationBell, QuickNewMenu, ThemeToggle, UserMenu } from "@/components/shared/Toolbar";
import { LanguageSwitcher } from "@/features/i18n/LanguageSwitcher";
import { useI18n } from "@/features/i18n/i18n";
import { useGet } from "@/hooks/useApi";
import { setActiveCurrency } from "@/lib/formatters";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { cn } from "@/lib/utils";
import { SearchPalette } from "@/features/search/SearchPalette";

function Navigation({
  compact = false,
  onNavigate,
}: {
  compact?: boolean;
  onNavigate?: () => void;
}) {
  const visible = useVisibleRoutes().filter((route) => route.navigation);
  const { t, groupLabel, routeLabel } = useI18n();
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  const sections = useMemo(() => {
    const out: { name: string | null; items: AppRoute[] }[] = [];
    let current: { name: string | null; items: AppRoute[] } = { name: null, items: [] };
    for (const r of visible) {
      if (r.group) {
        if (current.items.length > 0) out.push(current);
        current = { name: r.group, items: [r] };
      } else {
        current.items.push(r);
      }
    }
    if (current.items.length > 0) out.push(current);
    return out;
  }, [visible]);

  return (
    <nav aria-label={t.common.mainNavigation} className="space-y-0.5 pb-6">
      {sections.map((sec, idx) => {
        const isCollapsed = sec.name ? Boolean(collapsed[sec.name]) : false;
        return (
          <div key={sec.name ?? `root-${idx}`} className={idx > 0 && !compact ? "pt-3" : undefined}>
            {sec.name && !compact && (
              <button
                type="button"
                onClick={() => setCollapsed((c) => ({ ...c, [sec.name!]: !c[sec.name!] }))}
                className="mb-1 flex w-full items-center justify-between rounded-md px-2.5 py-1 text-[11px] font-semibold uppercase tracking-[0.04em] text-[var(--od-ink-muted)] transition-colors hover:text-[var(--od-ink)]"
              >
                <span>{groupLabel(sec.name)}</span>
                {isCollapsed ? <ChevronRight size={13} /> : <ChevronDown size={13} />}
              </button>
            )}
            {sec.name && compact && (
              <div className="mx-2 my-2 border-t border-[var(--od-border-strong)]" />
            )}
            {(!isCollapsed || compact) && (
              <div className="space-y-0.5">
                {sec.items.map(({ path, label, icon: Icon }) => {
                  const display = routeLabel(path, label);
                  return (
                    <NavLink
                      key={path}
                      end={path === "/"}
                      onClick={onNavigate}
                      to={path}
                      title={compact ? display : undefined}
                      className={({ isActive }) =>
                        cn(
                          "group flex items-center gap-2.5 rounded-[var(--od-app-radius,10px)] px-2.5 py-1.5 text-[13px] transition-colors",
                          compact && "justify-center px-0",
                          isActive
                            ? "bg-[var(--od-surface)] font-semibold text-[var(--od-ink)] shadow-[0_0_0_1px_var(--od-border-strong),var(--od-shadow)]"
                            : "font-medium text-[var(--od-ink-secondary)] hover:bg-[var(--od-surface-subtle)] hover:text-[var(--od-ink)]",
                        )
                      }
                    >
                      <Icon size={16} className="shrink-0 opacity-80 group-hover:opacity-100" />
                      {!compact && <span className="truncate">{display}</span>}
                    </NavLink>
                  );
                })}
              </div>
            )}
          </div>
        );
      })}
    </nav>
  );
}

export default function DashboardLayout() {
  const [compact, setCompact] = useState(false);
  const [open, setOpen] = useState(false);
  const location = useLocation();
  const { t, routeLabel } = useI18n();
  const business = useGet<{ name?: string; currency: string }>("/business");
  setActiveCurrency(business.data?.currency);
  const brand = business.data?.name || APP_NAME;
  const initials =
    brand
      .split(/\s+/)
      .slice(0, 2)
      .map((w) => w[0]?.toUpperCase() ?? "")
      .join("") || APP_NAME;

  const currentRoute = routes.find((r) =>
    matchPath({ path: r.path, end: true }, location.pathname),
  );
  const title = currentRoute ? routeLabel(currentRoute.path, currentRoute.label) : t.common.page;

  return (
    <div className="min-h-screen bg-[var(--od-page)] text-[var(--od-ink)]">
      {/* Desktop Collapsible Scrollable Sidebar */}
      <aside
        className={cn(
          "scrollbar-thin fixed inset-y-0 left-0 z-20 hidden overflow-y-auto border-r border-[var(--od-app-border)] bg-[var(--od-app-sidebar)] px-3 py-3.5 transition-[width] duration-200 md:block",
          compact ? "w-[68px]" : "w-60",
        )}
      >
        <Link
          to="/"
          className={cn(
            "mb-4 flex h-9 items-center gap-2.5 rounded-[var(--od-app-radius,10px)] px-2 transition-colors hover:bg-[var(--od-surface-subtle)]",
            compact && "justify-center px-0",
          )}
        >
          <span className="od-wordmark grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-[var(--od-app-primary)] text-[11px] font-bold text-[var(--od-app-primary-foreground)]">
            {initials}
          </span>
          {!compact && (
            <span className="od-wordmark truncate text-[15px] font-semibold text-[var(--od-ink)]">
              {brand}
            </span>
          )}
        </Link>
        <Navigation compact={compact} />
      </aside>

      {/* Main Content Area */}
      <div
        className={cn(
          "flex min-h-screen flex-col transition-[padding] duration-200",
          compact ? "md:pl-[68px]" : "md:pl-60",
        )}
      >
        <header className="sticky top-0 z-10 flex h-14 items-center gap-2.5 border-b border-[var(--od-app-border)] bg-[var(--od-page)]/90 px-4 backdrop-blur-md sm:px-6 lg:px-8">
          {/* Mobile Sheet Drawer */}
          <Sheet open={open} onOpenChange={setOpen}>
            <SheetTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="md:hidden"
                aria-label={t.common.openNavigation}
              >
                <Menu size={18} />
              </Button>
            </SheetTrigger>
            <SheetContent
              side="left"
              className="w-72 overflow-y-auto bg-[var(--od-app-sidebar)] px-3 py-4"
            >
              <SheetTitle className="od-wordmark mb-4 flex items-center gap-2.5 px-2 text-[15px] font-semibold">
                <span className="grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-[var(--od-app-primary)] text-[11px] font-bold text-[var(--od-app-primary-foreground)]">
                  {initials}
                </span>
                <span className="truncate">{brand}</span>
              </SheetTitle>
              <SheetDescription className="sr-only">
                {t.common.chooseAPageToNavigate}
              </SheetDescription>
              <Navigation onNavigate={() => setOpen(false)} />
            </SheetContent>
          </Sheet>

          {/* Sidebar Collapse Toggle */}
          <Button
            variant="ghost"
            size="icon"
            className="hidden md:inline-flex"
            aria-label={t.common.toggleSidebar}
            aria-expanded={!compact}
            onClick={() => setCompact(!compact)}
          >
            {compact ? <PanelLeft size={17} /> : <PanelLeftClose size={17} />}
          </Button>

          {/* Breadcrumbs */}
          <div className="hidden sm:block">
            <Breadcrumb>
              <BreadcrumbList className="text-[13px]">
                <BreadcrumbItem>
                  <BreadcrumbLink asChild>
                    <Link
                      to="/"
                      className="text-[var(--od-ink-muted)] transition-colors hover:text-[var(--od-ink)]"
                    >
                      {t.nav.home}
                    </Link>
                  </BreadcrumbLink>
                </BreadcrumbItem>
                <BreadcrumbSeparator />
                <BreadcrumbItem>
                  <BreadcrumbPage className="font-medium text-[var(--od-ink)]">
                    {title}
                  </BreadcrumbPage>
                </BreadcrumbItem>
              </BreadcrumbList>
            </Breadcrumb>
          </div>

          {/* Topbar Actions */}
          <div className="ml-auto flex items-center gap-1.5">
            <SearchPalette />
            <QuickNewMenu />
            <BranchSwitcher />
            <LanguageSwitcher />
            <ThemeToggle />
            <NotificationBell />
            <UserMenu />
          </div>
        </header>

        <main className="mx-auto w-full max-w-[1360px] flex-1 px-4 py-6 sm:px-6 lg:px-8">
          <Outlet />
        </main>

        <footer className="border-t border-[var(--od-border)] px-4 py-4 text-[12px] text-[var(--od-ink-muted)] sm:px-6 lg:px-8">
          <div className="mx-auto flex max-w-[1360px] items-center justify-between">
            <span className="od-wordmark font-medium text-[var(--od-ink-secondary)]">{brand}</span>
            <span>{t.common.businessManagementSystem}</span>
          </div>
        </footer>
      </div>

      {/* Mobile Floating + New Button */}
      <QuickNewMenu floating />
    </div>
  );
}
