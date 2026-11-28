import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Download, MessageCircle, Search, Send } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, FilterBar, PageHeader, SegmentedChips } from "@/components/shared/kit";
import { StatCard } from "@/components/shared/Display";
import { whatsappUrl } from "@/features/services/helpers";
import { useGet, useWrite } from "@/hooks/useApi";
import { ApiError } from "@/lib/api";
import { downloadCsv } from "@/lib/csv";
import { formatMoney } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";

interface DebtRow {
  id: string;
  name: string;
  phone: string;
  source: "shop_customer" | "service_client" | "supplier" | "contractor";
  link: string;
  d0_30: string;
  d31_60: string;
  over_60: string;
  total: string;
}

interface UnifiedDebtsRes {
  owed_to_us: DebtRow[];
  we_owe: DebtRow[];
}

const sourceLabel = (t: Translations): Record<DebtRow["source"], string> => ({
  shop_customer: t.common.shopCustomer,
  service_client: t.common.serviceClient,
  supplier: t.common.supplier,
  contractor: t.common.contractor,
});

function filterDebtRows(rows: DebtRow[], sourceFilter: string, search: string): DebtRow[] {
  const q = search.trim().toLowerCase();
  return rows.filter((r) => {
    if (sourceFilter && r.source !== sourceFilter) return false;
    if (q && !r.name.toLowerCase().includes(q) && !(r.phone ?? "").toLowerCase().includes(q)) {
      return false;
    }
    return true;
  });
}

export default function Debts() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const res = useGet<UnifiedDebtsRes>("/debts");
  const [tab, setTab] = useState("owed");
  const [sourceFilter, setSourceFilter] = useState("");
  const [search, setSearch] = useState("");
  const [waFallback, setWaFallback] = useState<Record<string, boolean>>({});

  const remind = useWrite<{ id: string; source: "shop_customer" | "service_client" }>(
    "POST",
    (b) =>
      b.source === "service_client"
        ? `/clients/${b.id}/debt-reminder`
        : `/customers/${b.id}/debt-reminder`,
    {
      success: t.finance.debtReminderQueued,
      toBody: () => ({ channel: "sms" }),
      onError: (err, vars) => {
        if (err instanceof ApiError && err.status === 503) {
          setWaFallback((m) => ({ ...m, [`${vars.source}-${vars.id}`]: true }));
        }
      },
    },
  );

  const owedToUs = useMemo(() => res.data?.owed_to_us ?? [], [res.data?.owed_to_us]);
  const weOwe = useMemo(() => res.data?.we_owe ?? [], [res.data?.we_owe]);

  const totalOwedToUs = useMemo(
    () => owedToUs.reduce((s, r) => s + Number(r.total || 0), 0),
    [owedToUs],
  );
  const totalWeOwe = useMemo(() => weOwe.reduce((s, r) => s + Number(r.total || 0), 0), [weOwe]);
  const overdueOwed = useMemo(
    () => owedToUs.reduce((s, r) => s + Number(r.over_60 || 0), 0),
    [owedToUs],
  );
  const netPosition = totalOwedToUs - totalWeOwe;

  const filteredOwed = useMemo(
    () => filterDebtRows(owedToUs, sourceFilter, search),
    [owedToUs, sourceFilter, search],
  );
  const filteredWeOwe = useMemo(
    () => filterDebtRows(weOwe, sourceFilter, search),
    [weOwe, sourceFilter, search],
  );

  function exportCsv(name: string, rows: DebtRow[]) {
    downloadCsv(name, [
      [
        t.common.name2,
        t.common.type,
        t.common.phone,
        "0-30 days",
        "31-60 days",
        ">60 days",
        t.common.total,
      ],
      ...rows.map((r) => [
        r.name,
        sourceLabel(t)[r.source],
        r.phone,
        r.d0_30,
        r.d31_60,
        r.over_60,
        r.total,
      ]),
    ]);
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.finance.debtsAging}
        description={t.finance.unifiedReceivablesAcrossShopServices}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              tab === "owed"
                ? exportCsv("owed-to-us.csv", filteredOwed)
                : exportCsv("we-owe.csv", filteredWeOwe)
            }
          >
            <Download size={14} /> {t.common.exportCsv}
          </Button>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label={t.common.owedToUs}
          value={formatMoney(totalOwedToUs)}
          trend={fmt(t.finance.accounts, { count: owedToUs.length })}
          tone="good"
        />
        <StatCard
          label={t.common.weOwe}
          value={formatMoney(totalWeOwe)}
          trend={fmt(t.finance.accounts, { count: weOwe.length })}
          tone={totalWeOwe > 0 ? "bad" : "neutral"}
        />
        <StatCard
          label={t.finance.receivables60Days}
          value={formatMoney(overdueOwed)}
          trend={overdueOwed > 0 ? t.finance.needsFollowUp : t.finance.noAgedBalances}
          tone={overdueOwed > 0 ? "bad" : "good"}
        />
        <StatCard
          label={t.finance.netPosition}
          value={formatMoney(netPosition)}
          trend={t.finance.receivablesMinusPayables}
          tone={netPosition >= 0 ? "good" : "bad"}
        />
      </div>

      <Tabs
        value={tab}
        onValueChange={(v) => {
          setTab(v);
          setSourceFilter("");
        }}
      >
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="owed">
              {fmt(t.finance.owedToUs, { count: owedToUs.length })}
            </TabsTrigger>
            <TabsTrigger value="we_owe">
              {fmt(t.finance.weOwe, { count: weOwe.length })}
            </TabsTrigger>
          </TabsList>

          <FilterBar className="mb-0">
            <div className="relative">
              <Search
                size={14}
                className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--od-ink-muted)]"
              />
              <Input
                className="w-56 pl-8"
                placeholder={t.common.searchNameOrPhone}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <SegmentedChips
              ariaLabel={t.finance.filterBySource}
              value={sourceFilter}
              onChange={setSourceFilter}
              options={
                tab === "owed"
                  ? [
                      { value: "", label: t.finance.allTypes },
                      { value: "shop_customer", label: t.common.shopCustomers },
                      { value: "service_client", label: t.common.serviceClients },
                    ]
                  : [
                      { value: "", label: t.finance.allTypes },
                      { value: "supplier", label: t.common.suppliers },
                      { value: "contractor", label: t.common.contractors },
                    ]
              }
            />
          </FilterBar>
        </div>

        <TabsContent value="owed">
          <DataGrid
            rows={filteredOwed}
            loading={res.isLoading}
            rowKey={(r) => `${r.source}-${r.id}`}
            empty={t.finance.noOutstandingBalancesOwedTo}
            onRowClick={(r) => navigate(r.link)}
            columns={[
              {
                header: t.common.name2,
                cell: (r) => (
                  <div>
                    <p className="font-medium text-[var(--od-ink)]">{r.name}</p>
                    {r.phone && (
                      <p className="od-mono text-xs text-[var(--od-ink-muted)]">{r.phone}</p>
                    )}
                  </div>
                ),
              },
              {
                header: t.common.source,
                cell: (r) => <Badge variant="secondary">{sourceLabel(t)[r.source]}</Badge>,
              },
              {
                header: t.common.n030Days,
                align: "right",
                cell: (r) =>
                  Number(r.d0_30) ? (
                    <span className="od-tabular">{formatMoney(r.d0_30)}</span>
                  ) : (
                    <span className="text-[var(--od-ink-muted)]">-</span>
                  ),
              },
              {
                header: t.finance.n3160Days,
                align: "right",
                cell: (r) =>
                  Number(r.d31_60) ? (
                    <span className="od-tabular font-medium text-[var(--od-warning)]">
                      {formatMoney(r.d31_60)}
                    </span>
                  ) : (
                    <span className="text-[var(--od-ink-muted)]">-</span>
                  ),
              },
              {
                header: t.finance.n60Days,
                align: "right",
                cell: (r) =>
                  Number(r.over_60) ? (
                    <span className="od-tabular font-semibold text-[var(--od-bad)]">
                      {formatMoney(r.over_60)}
                    </span>
                  ) : (
                    <span className="text-[var(--od-ink-muted)]">-</span>
                  ),
              },
              {
                header: t.finance.totalOwed,
                align: "right",
                cell: (r) => (
                  <span className="od-tabular font-semibold text-[var(--od-ink)]">
                    {formatMoney(r.total)}
                  </span>
                ),
              },
              {
                header: "",
                cell: (r) => {
                  const rowKey = `${r.source}-${r.id}`;
                  const canRemind =
                    (r.source === "shop_customer" || r.source === "service_client") &&
                    Boolean(r.phone);
                  const showWa = canRemind && Boolean(waFallback[rowKey]);
                  const wa = showWa
                    ? whatsappUrl(
                        r.phone,
                        `Hello ${r.name}, friendly reminder of your outstanding balance of ${formatMoney(r.total)}. Thank you!`,
                      )
                    : null;
                  return (
                    <div className="flex justify-end gap-1.5" onClick={(e) => e.stopPropagation()}>
                      {canRemind && (
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={remind.isPending}
                          onClick={() =>
                            remind.mutate({
                              id: r.id,
                              source: r.source as "shop_customer" | "service_client",
                            })
                          }
                        >
                          <Send size={13} /> {t.common.remind}
                        </Button>
                      )}
                      {wa && (
                        <Button size="sm" variant="outline" asChild>
                          <a href={wa} target="_blank" rel="noreferrer">
                            <MessageCircle size={13} /> {t.common.openInWhatsapp}
                          </a>
                        </Button>
                      )}
                      <Button size="sm" variant="ghost" onClick={() => navigate(r.link)}>
                        {t.common.open}
                      </Button>
                    </div>
                  );
                },
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="we_owe">
          <DataGrid
            rows={filteredWeOwe}
            loading={res.isLoading}
            rowKey={(r) => `${r.source}-${r.id}`}
            empty={t.finance.noOutstandingBalancesWeOwe}
            onRowClick={(r) => navigate(r.link)}
            columns={[
              {
                header: t.common.name2,
                cell: (r) => (
                  <div>
                    <p className="font-medium text-[var(--od-ink)]">{r.name}</p>
                    {r.phone && (
                      <p className="od-mono text-xs text-[var(--od-ink-muted)]">{r.phone}</p>
                    )}
                  </div>
                ),
              },
              {
                header: t.common.source,
                cell: (r) => <Badge variant="secondary">{sourceLabel(t)[r.source]}</Badge>,
              },
              {
                header: t.common.n030Days,
                align: "right",
                cell: (r) =>
                  Number(r.d0_30) ? (
                    <span className="od-tabular">{formatMoney(r.d0_30)}</span>
                  ) : (
                    <span className="text-[var(--od-ink-muted)]">-</span>
                  ),
              },
              {
                header: t.finance.n3160Days,
                align: "right",
                cell: (r) =>
                  Number(r.d31_60) ? (
                    <span className="od-tabular font-medium text-[var(--od-warning)]">
                      {formatMoney(r.d31_60)}
                    </span>
                  ) : (
                    <span className="text-[var(--od-ink-muted)]">-</span>
                  ),
              },
              {
                header: t.finance.n60Days,
                align: "right",
                cell: (r) =>
                  Number(r.over_60) ? (
                    <span className="od-tabular font-semibold text-[var(--od-bad)]">
                      {formatMoney(r.over_60)}
                    </span>
                  ) : (
                    <span className="text-[var(--od-ink-muted)]">-</span>
                  ),
              },
              {
                header: t.finance.totalOwed,
                align: "right",
                cell: (r) => (
                  <span className="od-tabular font-semibold text-[var(--od-ink)]">
                    {formatMoney(r.total)}
                  </span>
                ),
              },
              {
                header: "",
                cell: (r) => (
                  <div className="flex justify-end" onClick={(e) => e.stopPropagation()}>
                    <Button size="sm" variant="outline" onClick={() => navigate(r.link)}>
                      {t.finance.payView}
                    </Button>
                  </div>
                ),
              },
            ]}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}
