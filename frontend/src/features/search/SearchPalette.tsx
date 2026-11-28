import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Command as CommandPrimitive } from "cmdk";
import { History, LoaderCircle, Plus, RotateCw, Search, X, Zap } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";
import { useVisibleRoutes } from "@/app/useVisibleRoutes";
import { Button } from "@/components/ui/button";
import { Command, CommandGroup, CommandItem, CommandList } from "@/components/ui/command";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { useAuth } from "@/features/auth/auth";
import { useBusiness } from "@/features/business/useBusiness";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import { useGet } from "@/hooks/useApi";
import { get } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { Branch, Product } from "@/types";
import { isDocumentNumber, looksLikeBarcode, parseQuery } from "./query";
import { addRecent, readRecent, removeRecent, type RecentItem } from "./recent";
import { KIND_ICONS } from "./icons";
import { RecordRow, Row } from "./ResultRow";
import { DOCUMENT_KINDS, isExactMatch, listLink, PER_GROUP } from "./results";
import { SCOPES, type Scope } from "./scopes";
import { useQuickActions } from "./searchActions";
import { RECORD_KINDS, type RecordKind, type SearchHit } from "./types";
import { fetchSearch, isSearchable, useSearch } from "./useSearch";

/** The pages offered before anything is typed, most useful first for each role. */
const HOME_PAGES: Record<string, string[]> = {
  admin: ["/", "/reports", "/money", "/approvals", "/sales", "/team"],
  manager: ["/", "/pos", "/sales", "/approvals", "/projects", "/stock"],
  staff: ["/pos", "/sales", "/products", "/customers", "/service-tasks", "/my-work"],
};

interface Entry {
  value: string;
  content: ReactNode;
  run: (newTab: boolean) => void;
  /** Set for recent items, which can be removed. */
  recent?: string;
}
interface Section {
  id: string;
  heading: string;
  entries: Entry[];
}

const isTyping = (target: EventTarget | null) =>
  target instanceof HTMLElement &&
  (target.isContentEditable ||
    !!target.closest('input, textarea, select, [contenteditable="true"], [role="textbox"]'));

export function SearchPalette() {
  const { t, routeLabel } = useI18n();
  const { can, canAny, me, user, branchId } = useAuth();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const routes = useVisibleRoutes().filter((route) => route.navigation);
  const actions = useQuickActions().all;
  const userId = user?.id ?? "";

  const [open, setOpen] = useState(false);
  const [input, setInput] = useState("");
  const [scope, setScope] = useState<Scope>("all");
  const [everywhere, setEverywhere] = useState(false);
  const [selected, setSelected] = useState("");
  const [recent, setRecent] = useState<RecentItem[]>([]);
  const latestInput = useRef(input);
  latestInput.current = open ? input : "";

  const { mode, text } = parseQuery(input);
  const { shows } = useBusiness();
  const chips = SCOPES.filter(
    (s) => (!s.permissions.length || canAny(...s.permissions)) && shows(s.business),
  );
  const active = chips.find((s) => s.id === scope) ?? chips[0];
  const searchesRecords = mode !== "actions" && active.id !== "pages";
  const search = useSearch(
    {
      text: searchesRecords ? text : "",
      kind: active.kinds.length === 1 ? active.kinds[0] : undefined,
      everywhere,
    },
    open,
  );
  const branches = useGet<Branch[]>("/branches", undefined, { enabled: open });

  const change = (next: boolean) => {
    setOpen(next);
    if (next) setRecent(readRecent(userId));
    else {
      setInput("");
      setScope("all");
      setEverywhere(false);
    }
  };
  const openRef = useRef(change);
  openRef.current = change;

  useEffect(() => {
    const listener = (event: globalThis.KeyboardEvent) => {
      const shortcut = (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k";
      const slash =
        event.key === "/" && !event.ctrlKey && !event.metaKey && !isTyping(event.target);
      if (!shortcut && !slash) return;
      event.preventDefault();
      openRef.current(true);
    };
    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, []);

  const go = (link: string, newTab: boolean, remember?: RecentItem) => {
    if (remember) setRecent(addRecent(userId, remember));
    change(false);
    if (newTab) window.open(link, "_blank", "noopener");
    else navigate(link);
  };

  const openRecord = (hit: SearchHit, newTab: boolean, typed: string) => {
    const remember: RecentItem = {
      type: "record",
      kind: hit.kind,
      title: hit.title,
      subtitle: hit.subtitle,
      link: hit.link,
    };
    // At the till, scanning a product's own code puts it in the cart.
    if (
      !newTab &&
      pathname === "/pos" &&
      hit.kind === "product" &&
      can("shop.sale.create") &&
      isExactMatch(hit, typed)
    ) {
      change(false);
      navigate(`/pos?add=${encodeURIComponent(hit.id)}`, { replace: true });
      return;
    }
    go(hit.link, newTab, remember);
  };

  /** Enter arrived before the results did, as it does from a barcode scanner. */
  const openCode = async (code: string, newTab: boolean) => {
    const typed = latestInput.current;
    const stillWanted = () => latestInput.current === typed;
    if (!isDocumentNumber(code) && can("shop.product.view")) {
      try {
        const product = await get<Product>("/products/lookup", { barcode: code });
        if (product?.id && stillWanted()) {
          openRecord(
            {
              kind: "product",
              id: product.id,
              title: product.name,
              subtitle: product.barcode ?? product.sku,
              link: `/products/${product.id}`,
              meta: { barcode: product.barcode ?? code },
            },
            newTab,
            code,
          );
          return;
        }
      } catch {
        // Not a barcode we know: try it as ordinary search text.
      }
    }
    try {
      const result = await fetchSearch({ text: code, everywhere });
      const hit =
        result.best ??
        result.groups.flatMap((g) => g.hits).find((h) => isExactMatch(h, code)) ??
        null;
      if (hit && stillWanted()) openRecord(hit, newTab, code);
    } catch {
      // The live search shows the error and a retry button.
    }
  };

  // ---- what the list shows -------------------------------------------------------------
  const matches = (...names: string[]) => {
    const words = text.toLowerCase().split(/\s+/).filter(Boolean);
    const haystack = names.join(" ").toLowerCase();
    return words.every((word) => haystack.includes(word));
  };
  const pageEntry = (route: (typeof routes)[number], prefix: string): Entry => {
    const Icon = route.icon;
    return {
      value: `${prefix}:${route.path}`,
      content: (
        <Row icon={<Icon size={15} />} title={routeLabel(route.path, route.label)} query={text} />
      ),
      run: (newTab) => go(route.path, newTab),
    };
  };
  const actionEntry = (action: (typeof actions)[number]): Entry => ({
    value: `action:${action.id}`,
    content: <Row icon={<Zap size={15} />} title={action.label} query={text} />,
    run: (newTab) => go(action.to, newTab),
  });
  const recordEntry = (hit: SearchHit, prefix = "hit"): Entry => ({
    value: `${prefix}:${hit.kind}:${hit.id}`,
    content: <RecordRow hit={hit} query={text} />,
    run: (newTab) => openRecord(hit, newTab, text),
  });
  const searchEntry = (value: string, label: string, link: string, icon: ReactNode): Entry => ({
    value,
    content: <Row icon={icon} title={label} />,
    run: (newTab) => go(link, newTab, { type: "search", text, link }),
  });

  const inScope = (kind: RecordKind) =>
    (!active.kinds.length || active.kinds.includes(kind)) &&
    (mode !== "documents" || DOCUMENT_KINDS.includes(kind));
  const numberMatches = (hit: SearchHit) =>
    mode !== "documents" || hit.title.toLowerCase().includes(text.toLowerCase());
  const found = searchesRecords ? search.result : undefined;
  const best = found?.best && inScope(found.best.kind) ? found.best : null;
  const groups = RECORD_KINDS.flatMap((kind) => {
    const group = found?.groups.find((g) => g.kind === kind);
    if (!group || !inScope(kind)) return [];
    const hits = group.hits
      .filter((h) => numberMatches(h) && !(best && h.kind === best.kind && h.id === best.id))
      .slice(0, PER_GROUP);
    return hits.length ? [{ ...group, hits }] : [];
  });

  const sections: Section[] = [];
  const add = (id: string, heading: string, entries: Entry[]) => {
    if (entries.length) sections.push({ id, heading, entries });
  };

  if (!text && mode === "all" && active.id !== "pages") {
    add(
      "recent",
      t.search.groups.recent,
      recent.map((item) => {
        const Icon = item.type === "record" ? (KIND_ICONS[item.kind] ?? History) : History;
        const name =
          item.type === "record" ? item.title : fmt(t.search.recentSearch, { query: item.text });
        return {
          value: `recent:${item.link}`,
          recent: item.link,
          content: (
            <>
              <Row
                icon={<Icon size={15} />}
                title={name}
                subtitle={item.type === "record" ? item.subtitle : undefined}
              />
              <button
                type="button"
                tabIndex={-1}
                className="shrink-0 rounded-md p-1.5 text-[var(--od-ink-muted)] hover:bg-[var(--od-surface)] hover:text-[var(--od-ink)]"
                aria-label={fmt(t.search.removeRecent, { name })}
                onClick={(event) => {
                  event.stopPropagation();
                  setRecent(removeRecent(userId, item.link));
                }}
              >
                <X size={13} />
              </button>
            </>
          ),
          run: (newTab: boolean) => go(item.link, newTab, item),
        };
      }),
    );
    if (active.id === "all") {
      add("quick", t.search.groups.quick, actions.map(actionEntry));
      const preferred = HOME_PAGES[user?.role_slug ?? ""] ?? HOME_PAGES.staff;
      const home = [
        ...preferred.flatMap((path) => routes.filter((r) => r.path === path)),
        ...routes.filter((r) => !preferred.includes(r.path)),
      ].slice(0, 6);
      add(
        "goto",
        t.search.groups.goTo,
        home.map((r) => pageEntry(r, "goto")),
      );
    }
  } else {
    if (best) add("best", t.search.groups.best, [recordEntry(best, "best")]);
    for (const group of groups) {
      const entries = group.hits.map((hit) => recordEntry(hit));
      if (group.total > group.hits.length + (best?.kind === group.kind ? 1 : 0))
        entries.push(
          searchEntry(
            `all:${group.kind}`,
            fmt(t.search.seeAll, { count: group.total }),
            listLink(group.kind, text),
            <Search size={15} />,
          ),
        );
      add(group.kind, t.search.groups[group.kind], entries);
    }
    if (search.error)
      add("error", "", [
        {
          value: "retry",
          content: <Row icon={<RotateCw size={15} />} title={t.search.retry} />,
          run: () => search.retry(),
        },
      ]);
    if (searchesRecords && search.settled && !best && !groups.length && mode === "all") {
      const name = encodeURIComponent(text);
      add(
        "create",
        t.search.groups.actions,
        [
          can("services.client.manage") &&
            searchEntry(
              "create:client",
              fmt(t.search.createClient, { query: text }),
              `/clients?new=1&name=${name}`,
              <Plus size={15} />,
            ),
          can("shop.product.manage") &&
            searchEntry(
              "create:product",
              fmt(t.search.createProduct, { query: text }),
              `/products?new=1&name=${name}`,
              <Plus size={15} />,
            ),
          canAny("shop.sale.view_all", "shop.sale.create") &&
            searchEntry(
              "search:sales",
              t.search.searchSales,
              listLink("sale", text),
              <Search size={15} />,
            ),
        ].filter((entry): entry is Entry => !!entry),
      );
    }
    if (mode === "all" && (active.id === "all" || active.id === "pages")) {
      const pages = routes.filter((r) => matches(routeLabel(r.path, r.label), r.label));
      add(
        "pages",
        t.search.groups.pages,
        (active.id === "pages" ? pages : pages.slice(0, PER_GROUP)).map((r) =>
          pageEntry(r, "page"),
        ),
      );
    }
    if (mode === "actions" || (mode === "all" && active.id === "all")) {
      const wanted = actions.filter((a) => matches(a.label, a.keywords));
      add(
        "actions",
        t.search.groups.actions,
        (mode === "actions" ? wanted : wanted.slice(0, PER_GROUP)).map(actionEntry),
      );
    }
  }

  const entries = sections.flatMap((s) => s.entries);
  const signature = entries.map((e) => e.value).join("|");
  const first = entries[0]?.value ?? "";
  // A new list starts at its top, so Enter opens the best answer.
  useEffect(() => setSelected(first), [signature, first]);

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.nativeEvent.isComposing) return;
    if (event.key === "Tab") {
      event.preventDefault();
      const at = chips.findIndex((c) => c.id === active.id);
      setScope(chips[(at + (event.shiftKey ? chips.length - 1 : 1)) % chips.length].id);
      return;
    }
    const current = entries.find((e) => e.value === selected);
    if (event.key === "Delete" && !input && current?.recent) {
      event.preventDefault();
      setRecent(removeRecent(userId, current.recent));
      return;
    }
    if (event.key !== "Enter") return;
    const newTab = event.ctrlKey || event.metaKey;
    const isCode = isDocumentNumber(text) || looksLikeBarcode(text);
    if (searchesRecords && isCode && !search.settled) {
      event.preventDefault();
      void openCode(text, newTab);
    } else if (newTab && current) {
      event.preventDefault();
      current.run(true);
    }
  };

  const places = (branches.data ?? []).filter((b) => b.is_active);
  const oneBranch = branchId && branchId !== "all" ? branchId : null;
  const place =
    !everywhere && oneBranch
      ? (places.find((b) => b.id === oneBranch)?.name ?? t.common.allLocations)
      : t.common.allLocations;
  // The server only lets people with access to every branch search all of them.
  const maySearchEverywhere = Boolean(me?.is_owner || me?.branches?.length === 0);
  const nothing = searchesRecords && search.settled && !best && !groups.length && text.length > 0;
  const waitingFirst = searchesRecords && search.loading && !search.result;

  return (
    <Dialog open={open} onOpenChange={change}>
      <DialogTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="h-8 gap-2 border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] px-2.5 text-[var(--od-ink-muted)] shadow-none hover:text-[var(--od-ink)]"
          aria-label={t.search.open}
        >
          <Search size={14} />
          <span className="hidden whitespace-nowrap text-xs 2xl:inline">
            {t.common.globalSearchPlaceholder}
          </span>
          <kbd className="od-mono hidden whitespace-nowrap rounded border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--od-ink-muted)] xl:inline">
            Ctrl K
          </kbd>
        </Button>
      </DialogTrigger>
      <DialogContent
        className={cn(
          "flex flex-col gap-0 overflow-hidden p-0 [&>button]:hidden",
          // Phones: the whole screen, with the box at the top.
          "max-sm:left-0 max-sm:top-0 max-sm:h-dvh max-sm:w-full max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-none max-sm:border-0",
          "sm:top-[15vh] sm:max-h-[70vh] sm:max-w-[640px] sm:translate-y-0",
        )}
      >
        <DialogTitle className="sr-only">{t.search.title}</DialogTitle>
        <DialogDescription className="sr-only">{t.search.description}</DialogDescription>
        <Command
          shouldFilter={false}
          loop
          label={t.search.title}
          value={selected}
          onValueChange={setSelected}
          onKeyDown={onKeyDown}
          className="min-h-0 bg-transparent"
        >
          <div className="flex items-center gap-2 border-b border-[var(--od-border)] px-3">
            {search.loading ? (
              <LoaderCircle
                size={16}
                className="shrink-0 animate-spin text-[var(--od-ink-muted)]"
                aria-label={t.search.loading}
              />
            ) : (
              <Search size={16} className="shrink-0 text-[var(--od-ink-muted)]" aria-hidden />
            )}
            <CommandPrimitive.Input
              autoFocus
              value={input}
              onValueChange={setInput}
              placeholder={t.search.placeholder}
              className="h-12 min-w-0 flex-1 bg-transparent text-[16px] outline-none placeholder:text-[var(--od-ink-muted)] sm:text-sm"
            />
            {active.id !== "all" && (
              <span className="shrink-0 rounded-full bg-[var(--od-neutral-soft)] px-2 py-0.5 text-[11px] font-medium">
                {t.search.scopes[active.id]}
              </span>
            )}
            <kbd className="od-mono hidden shrink-0 rounded border border-[var(--od-border-strong)] px-1.5 py-0.5 text-[10px] text-[var(--od-ink-muted)] sm:inline">
              Esc
            </kbd>
            <button
              type="button"
              className="shrink-0 rounded-lg p-2 text-[var(--od-ink-muted)] sm:hidden"
              aria-label={t.search.close}
              onClick={() => change(false)}
            >
              <X size={18} />
            </button>
          </div>

          <div
            role="group"
            aria-label={t.search.scopeLabel}
            className="flex shrink-0 gap-1 overflow-x-auto border-b border-[var(--od-border)] px-3 py-2"
          >
            {chips.map((chip) => (
              <button
                key={chip.id}
                type="button"
                tabIndex={-1}
                aria-pressed={chip.id === active.id}
                className={cn(
                  "shrink-0 rounded-full border px-2.5 py-1 text-xs font-medium",
                  chip.id === active.id
                    ? "border-transparent bg-primary text-primary-foreground"
                    : "border-[var(--od-border-strong)] text-[var(--od-ink-secondary)]",
                )}
                // Keep the cursor in the search box.
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => setScope(chip.id)}
              >
                {t.search.scopes[chip.id]}
              </button>
            ))}
          </div>

          <CommandList className="max-h-none min-h-0 flex-1 pb-1">
            {search.error && (
              <p role="alert" className="px-4 pt-3 text-[13px] text-[var(--od-bad)]">
                {t.search.failed}
              </p>
            )}
            {nothing && (
              <p role="status" className="px-4 pt-4 text-[13px] text-[var(--od-ink-secondary)]">
                {fmt(t.search.nothing, { query: text })}
              </p>
            )}
            {mode === "all" && searchesRecords && text.length > 0 && !isSearchable(text) && (
              <p className="px-4 pt-3 text-xs text-[var(--od-ink-muted)]">{t.search.keepTyping}</p>
            )}
            {waitingFirst && (
              <div data-testid="search-loading" aria-hidden className="space-y-3 px-4 py-3">
                {[0, 1, 2].map((group) => (
                  <div key={group} className="space-y-2">
                    <div className="h-2.5 w-20 animate-pulse rounded bg-[var(--od-surface-subtle)]" />
                    <div className="h-8 animate-pulse rounded-lg bg-[var(--od-surface-subtle)]" />
                  </div>
                ))}
              </div>
            )}
            {found?.groups.some((g) => g.partial) && (
              <p className="px-4 pt-3 text-xs text-[var(--od-warning)]">{t.search.partial}</p>
            )}
            {sections.map((section) => (
              <CommandGroup key={section.id} heading={section.heading || undefined}>
                {section.entries.map((entry) => (
                  <CommandItem
                    key={entry.value}
                    value={entry.value}
                    onSelect={() => entry.run(false)}
                    // Caught on the way down: the list item replaces any plain click handler.
                    onClickCapture={(event) => {
                      // Ctrl or ⌘ click behaves like a link.
                      if (!event.ctrlKey && !event.metaKey) return;
                      event.preventDefault();
                      event.stopPropagation();
                      entry.run(true);
                    }}
                    className="min-h-11 cursor-pointer gap-3 rounded-[var(--od-radius-sm,8px)] px-2 py-1.5"
                  >
                    {entry.content}
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
          </CommandList>

          <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t border-[var(--od-border)] px-3 py-2 text-[11px] text-[var(--od-ink-muted)]">
            {places.length > 1 && (
              // TODO(business-scope): name the active business (All / Shop / Services) here and
              // send it with the search once TASK_business_division.md adds it.
              <span>
                {fmt(t.search.searchingIn, { place })}
                {maySearchEverywhere && oneBranch && (
                  <button
                    type="button"
                    tabIndex={-1}
                    className="ml-2 font-medium text-[var(--od-info)] underline-offset-2 hover:underline"
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => setEverywhere((value) => !value)}
                  >
                    {everywhere ? t.search.thisPlace : t.search.everywhere}
                  </button>
                )}
              </span>
            )}
            <span className="hidden sm:inline">
              ↑↓ {t.search.hints.navigate} · ↵ {t.search.hints.open} · Ctrl+↵{" "}
              {t.search.hints.newTab} · Tab {t.search.hints.scope} · Esc {t.search.hints.close}
            </span>
          </div>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
