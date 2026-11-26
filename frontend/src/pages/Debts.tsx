import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Download, MessageCircle, Send } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, NativeSelect, PageHeader } from "@/components/shared/kit";
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

  const owedToUs = res.data?.owed_to_us ?? [];
  const weOwe = res.data?.we_owe ?? [];

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

  const filterRows = (rows: DebtRow[]) => {
    const q = search.trim().toLowerCase();
    return rows.filter((r) => {
      if (sourceFilter && r.source !== sourceFilter) return false;
      if (q && !r.name.toLowerCase().includes(q) && !(r.phone ?? "").toLowerCase().includes(q)) {
        return false;
      }
      return true;
    });
  };

  const filteredOwed = useMemo(() => filterRows(owedToUs), [owedToUs, sourceFilter, search]);
  const filteredWeOwe = useMemo(() => filterRows(weOwe), [weOwe, sourceFilter, search]);

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
    <>
      <PageHeader
        title="Debts & Aging"
        description="Unified receivables across Shop & Services, and payables to Suppliers & Contractors."
      />

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border bg-card p-4 shadow-sm">
          <p className="text-xs uppercase tracking-wider text-muted-foreground">Owed to us</p>
          <p className="mt-1 text-2xl font-bold tabular-nums text-emerald-700">
            {formatMoney(totalOwedToUs)}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">{owedToUs.length} accounts</p>
        </div>
        <div className="rounded-xl border bg-card p-4 shadow-sm">
          <p className="text-xs uppercase tracking-wider text-muted-foreground">We owe</p>
          <p className="mt-1 text-2xl font-bold tabular-nums text-rose-700">
            {formatMoney(totalWeOwe)}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">{weOwe.length} accounts</p>
        </div>
        <div className="rounded-xl border bg-card p-4 shadow-sm">
          <p className="text-xs uppercase tracking-wider text-muted-foreground">
            Receivables &gt; 60 days
          </p>
          <p className="mt-1 text-2xl font-bold tabular-nums text-amber-700">
            {formatMoney(overdueOwed)}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">Needs follow-up</p>
        </div>
        <div className="rounded-xl border bg-card p-4 shadow-sm">
          <p className="text-xs uppercase tracking-wider text-muted-foreground">Net position</p>
          <p className="mt-1 text-2xl font-bold tabular-nums">{formatMoney(netPosition)}</p>
          <p className="mt-1 text-xs text-muted-foreground">Receivables minus payables</p>
        </div>
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

          <div className="flex flex-wrap items-center gap-2">
            <Input
              className="w-48"
              placeholder="Search name or phone…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <NativeSelect
              className="w-40"
              value={sourceFilter}
              onChange={(e) => setSourceFilter(e.target.value)}
            >
              <option value="">All types</option>
              {tab === "owed" ? (
                <>
                  <option value="shop_customer">Shop customers</option>
                  <option value="service_client">Service clients</option>
                </>
              ) : (
                <>
                  <option value="supplier">Suppliers</option>
                  <option value="contractor">Contractors</option>
                </>
              )}
            </NativeSelect>
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
          </div>
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
                    <p className="font-medium">{r.name}</p>
                    {r.phone && <p className="text-xs text-muted-foreground">{r.phone}</p>}
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
                cell: (r) => (Number(r.d0_30) ? formatMoney(r.d0_30) : "-"),
              },
              {
                header: "31–60 days",
                align: "right",
                cell: (r) =>
                  Number(r.d31_60) ? (
                    <span className="text-amber-700">{formatMoney(r.d31_60)}</span>
                  ) : (
                    "-"
                  ),
              },
              {
                header: "> 60 days",
                align: "right",
                cell: (r) =>
                  Number(r.over_60) ? (
                    <span className="font-semibold text-rose-700">{formatMoney(r.over_60)}</span>
                  ) : (
                    "-"
                  ),
              },
              {
                header: "Total owed",
                align: "right",
                cell: (r) => <span className="font-semibold">{formatMoney(r.total)}</span>,
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
                    <div className="flex justify-end gap-1" onClick={(e) => e.stopPropagation()}>
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
                    <p className="font-medium">{r.name}</p>
                    {r.phone && <p className="text-xs text-muted-foreground">{r.phone}</p>}
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
                cell: (r) => (Number(r.d0_30) ? formatMoney(r.d0_30) : "-"),
              },
              {
                header: "31–60 days",
                align: "right",
                cell: (r) =>
                  Number(r.d31_60) ? (
                    <span className="text-amber-700">{formatMoney(r.d31_60)}</span>
                  ) : (
                    "-"
                  ),
              },
              {
                header: "> 60 days",
                align: "right",
                cell: (r) =>
                  Number(r.over_60) ? (
                    <span className="font-semibold text-rose-700">{formatMoney(r.over_60)}</span>
                  ) : (
                    "-"
                  ),
              },
              {
                header: "Total owed",
                align: "right",
                cell: (r) => <span className="font-semibold">{formatMoney(r.total)}</span>,
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
    </>
  );
}
