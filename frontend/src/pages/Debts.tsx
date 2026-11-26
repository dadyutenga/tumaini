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

const SOURCE_LABEL: Record<DebtRow["source"], string> = {
  shop_customer: "Shop customer",
  service_client: "Service client",
  supplier: "Supplier",
  contractor: "Contractor",
};

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
      success: "Debt reminder queued",
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
      ["Name", "Type", "Phone", "0-30 days", "31-60 days", ">60 days", "Total"],
      ...rows.map((r) => [
        r.name,
        SOURCE_LABEL[r.source],
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
        title="Debts & Aging"
        description="Unified receivables across Shop & Services, and payables to Suppliers & Contractors."
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
            <Download size={14} /> Export CSV
          </Button>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Owed to us"
          value={formatMoney(totalOwedToUs)}
          trend={`${owedToUs.length} accounts`}
          tone="good"
        />
        <StatCard
          label="We owe"
          value={formatMoney(totalWeOwe)}
          trend={`${weOwe.length} accounts`}
          tone={totalWeOwe > 0 ? "bad" : "neutral"}
        />
        <StatCard
          label="Receivables > 60 days"
          value={formatMoney(overdueOwed)}
          trend={overdueOwed > 0 ? "Needs follow-up" : "No aged balances"}
          tone={overdueOwed > 0 ? "bad" : "good"}
        />
        <StatCard
          label="Net position"
          value={formatMoney(netPosition)}
          trend="Receivables minus payables"
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
            <TabsTrigger value="owed">Owed to us ({owedToUs.length})</TabsTrigger>
            <TabsTrigger value="we_owe">We owe ({weOwe.length})</TabsTrigger>
          </TabsList>

          <FilterBar className="mb-0">
            <div className="relative">
              <Search
                size={14}
                className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--od-ink-muted)]"
              />
              <Input
                className="w-56 pl-8"
                placeholder="Search name or phone…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
            <SegmentedChips
              ariaLabel="Filter by source"
              value={sourceFilter}
              onChange={setSourceFilter}
              options={
                tab === "owed"
                  ? [
                      { value: "", label: "All types" },
                      { value: "shop_customer", label: "Shop customers" },
                      { value: "service_client", label: "Service clients" },
                    ]
                  : [
                      { value: "", label: "All types" },
                      { value: "supplier", label: "Suppliers" },
                      { value: "contractor", label: "Contractors" },
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
            empty="No outstanding balances owed to you"
            onRowClick={(r) => navigate(r.link)}
            columns={[
              {
                header: "Name",
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
                header: "Source",
                cell: (r) => <Badge variant="secondary">{SOURCE_LABEL[r.source]}</Badge>,
              },
              {
                header: "0–30 days",
                align: "right",
                cell: (r) =>
                  Number(r.d0_30) ? (
                    <span className="od-tabular">{formatMoney(r.d0_30)}</span>
                  ) : (
                    <span className="text-[var(--od-ink-muted)]">-</span>
                  ),
              },
              {
                header: "31–60 days",
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
                header: "> 60 days",
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
                header: "Total owed",
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
                          <Send size={13} /> Remind
                        </Button>
                      )}
                      {wa && (
                        <Button size="sm" variant="outline" asChild>
                          <a href={wa} target="_blank" rel="noreferrer">
                            <MessageCircle size={13} /> Open in WhatsApp
                          </a>
                        </Button>
                      )}
                      <Button size="sm" variant="ghost" onClick={() => navigate(r.link)}>
                        Open
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
            empty="No outstanding balances we owe"
            onRowClick={(r) => navigate(r.link)}
            columns={[
              {
                header: "Name",
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
                header: "Source",
                cell: (r) => <Badge variant="secondary">{SOURCE_LABEL[r.source]}</Badge>,
              },
              {
                header: "0–30 days",
                align: "right",
                cell: (r) =>
                  Number(r.d0_30) ? (
                    <span className="od-tabular">{formatMoney(r.d0_30)}</span>
                  ) : (
                    <span className="text-[var(--od-ink-muted)]">-</span>
                  ),
              },
              {
                header: "31–60 days",
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
                header: "> 60 days",
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
                header: "Total owed",
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
                      Pay / view
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
