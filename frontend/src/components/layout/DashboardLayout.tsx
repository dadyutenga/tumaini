import { APP_NAME } from "@/lib/constants";
import { useMemo, useState } from "react";
import { Link, NavLink, Outlet, matchPath, useLocation } from "react-router-dom";
import { ChevronDown, ChevronRight, Menu, PanelLeftClose } from "lucide-react";
import { routes, type AppRoute } from "@/app/routes";
import { useVisibleRoutes } from "@/app/useVisibleRoutes";
import { BranchSwitcher } from "@/components/layout/BranchSwitcher";
import {
  NotificationBell,
  QuickNewMenu,
  SearchPalette,
  ThemeToggle,
  UserMenu,
} from "@/components/shared/Toolbar";
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

function Navigation({
  compact = false,
  onNavigate,
}: {
  compact?: boolean;
  onNavigate?: () => void;
}) {
  const visible = useVisibleRoutes().filter((route) => route.navigation);
  const { groupLabel, routeLabel } = useI18n();
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
    <nav aria-label="Main navigation" className="space-y-1 pb-6">
      {sections.map((sec, idx) => {
        const isCollapsed = sec.name ? Boolean(collapsed[sec.name]) : false;
        return (
          <div key={sec.name ?? `root-${idx}`}>
            {sec.name && !compact && (
              <button
                type="button"
                onClick={() => setCollapsed((c) => ({ ...c, [sec.name!]: !c[sec.name!] }))}
                className="mt-4 flex w-full items-center justify-between px-3 pb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground/80 hover:text-foreground"
              >
                <span>{groupLabel(sec.name)}</span>
                {isCollapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
              </button>
            )}
            {sec.name && compact && <div className="my-2 border-t" />}
            {(!isCollapsed || compact) &&
              sec.items.map(({ path, label, icon: Icon }) => {
                const display = routeLabel(path, label);
                return (
                  <NavLink
                    key={path}
                    end={path === "/"}
                    onClick={onNavigate}
                    to={path}
                    title={compact ? display : undefined}
                    className={({ isActive }) =>
                      `flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium transition ${
                        isActive
                          ? "bg-primary/15 text-primary"
                          : "text-muted-foreground hover:bg-muted hover:text-foreground"
                      }`
                    }
                  >
                    <Icon size={18} className="shrink-0" />
                    {!compact && <span className="truncate">{display}</span>}
                  </NavLink>
                );
              })}
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
  const title = currentRoute ? routeLabel(currentRoute.path, currentRoute.label) : "Page";

  return (
    <div className="min-h-screen bg-background text-foreground">
      {/* Desktop Collapsible Scrollable Sidebar */}
      <aside
        className={`fixed inset-y-0 left-0 z-20 hidden overflow-y-auto border-r bg-card p-4 transition-all md:block ${
          compact ? "w-20" : "w-64"
        }`}
      >
        <Link to="/" className="mb-6 flex h-10 items-center gap-3 px-2 font-bold">
          <span className="grid h-9 w-9 shrink-0 place-items-center rounded-xl bg-primary text-xs font-black text-primary-foreground">
            {initials}
          </span>
          {!compact && <span className="truncate text-base tracking-tight">{brand}</span>}
        </Link>
        <Navigation compact={compact} />
      </aside>

      {/* Main Content Area */}
      <div className={compact ? "md:pl-20" : "md:pl-64"}>
        <header className="sticky top-0 z-10 flex h-16 items-center gap-3 border-b bg-background/95 px-4 backdrop-blur sm:px-8">
          {/* Mobile Sheet Drawer */}
          <Sheet open={open} onOpenChange={setOpen}>
            <SheetTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="md:hidden"
                aria-label="Open navigation"
              >
                <Menu />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="overflow-y-auto">
              <SheetTitle className="mb-4">{brand}</SheetTitle>
              <SheetDescription className="sr-only">Choose a page to navigate to.</SheetDescription>
              <Navigation onNavigate={() => setOpen(false)} />
            </SheetContent>
          </Sheet>

          {/* Sidebar Collapse Toggle */}
          <Button
            variant="ghost"
            size="icon"
            className="hidden md:inline-flex"
            aria-label="Toggle sidebar"
            aria-expanded={!compact}
            onClick={() => setCompact(!compact)}
          >
            <PanelLeftClose />
          </Button>

          {/* Breadcrumbs */}
          <div className="hidden sm:block">
            <Breadcrumb>
              <BreadcrumbList>
                <BreadcrumbItem>
                  <BreadcrumbLink asChild>
                    <Link to="/">{t.nav.home}</Link>
                  </BreadcrumbLink>
                </BreadcrumbItem>
                <BreadcrumbSeparator />
                <BreadcrumbItem>
                  <BreadcrumbPage>{title}</BreadcrumbPage>
                </BreadcrumbItem>
              </BreadcrumbList>
            </Breadcrumb>
          </div>

          {/* Topbar Actions */}
          <div className="ml-auto flex items-center gap-1.5 sm:gap-2">
            <SearchPalette />
            <QuickNewMenu />
            <BranchSwitcher />
            <LanguageSwitcher />
            <ThemeToggle />
            <NotificationBell />
            <UserMenu />
          </div>
        </header>

        <main className="mx-auto max-w-7xl p-4 sm:p-8">
          <Outlet />
        </main>

        <footer className="border-t px-8 py-6 text-xs text-muted-foreground">{brand}</footer>
      </div>

      {/* Mobile Floating + New Button (SRS Section 3) */}
      <QuickNewMenu floating />
    </div>
  );
}
