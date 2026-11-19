import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/shared/kit";
import { useGet } from "@/hooks/useApi";
import { formatMoney, toNumber } from "@/lib/formatters";
import { blankLine, linesTotal, type LineDraft } from "@/features/services/lines";
import type { CatalogItem, DocLine } from "@/types/services";

/** Editable list of invoice or quotation lines. Picking a service fills the description and price. */
export function LinesEditor({
  lines,
  onChange,
}: {
  lines: LineDraft[];
  onChange: (lines: LineDraft[]) => void;
}) {
  const catalog = useGet<CatalogItem[]>("/service-catalog", { per_page: 100 });
  const patch = (i: number, p: Partial<LineDraft>) =>
    onChange(lines.map((l, j) => (j === i ? { ...l, ...p } : l)));
  return (
    <div className="space-y-3">
      {lines.map((l, i) => (
        <div key={i} className="grid gap-2 rounded-xl border p-3 sm:grid-cols-[1fr_5rem_8rem_auto]">
          <div className="space-y-2">
            <NativeSelect
              aria-label="Service"
              value={l.service_id}
              onChange={(e) => {
                const s = catalog.data?.find((x) => x.id === e.target.value);
                patch(i, {
                  service_id: e.target.value,
                  description: s ? s.name : l.description,
                  unit_price: s ? s.standard_price : l.unit_price,
                });
              }}
            >
              <option value="">Other (type below)</option>
              {catalog.data
                ?.filter((s) => s.is_active)
                .map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
            </NativeSelect>
            <Input
              aria-label="Description"
              placeholder="Description"
              required
              value={l.description}
              onChange={(e) => patch(i, { description: e.target.value })}
            />
          </div>
          <Input
            aria-label="Quantity"
            inputMode="decimal"
            value={l.quantity}
            onChange={(e) => patch(i, { quantity: e.target.value })}
          />
          <Input
            aria-label="Unit price"
            inputMode="decimal"
            placeholder="Price"
            required
            value={l.unit_price}
            onChange={(e) => patch(i, { unit_price: e.target.value })}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Remove line"
            disabled={lines.length === 1}
            onClick={() => onChange(lines.filter((_, j) => j !== i))}
          >
            <Trash2 size={14} />
          </Button>
        </div>
      ))}
      <div className="flex items-center justify-between">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...lines, { ...blankLine }])}
        >
          <Plus size={14} /> Add line
        </Button>
        <p className="text-sm">
          Subtotal <strong>{formatMoney(linesTotal(lines))}</strong>
        </p>
      </div>
    </div>
  );
}

/** Read-only table of lines for detail views. */
export function LinesTable({ lines }: { lines: DocLine[] }) {
  return (
    <div className="overflow-x-auto rounded-xl border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-left text-muted-foreground">
            <th className="px-3 py-2 font-medium">Description</th>
            <th className="px-3 py-2 text-right font-medium">Qty</th>
            <th className="px-3 py-2 text-right font-medium">Price</th>
            <th className="px-3 py-2 text-right font-medium">Total</th>
          </tr>
        </thead>
        <tbody>
          {lines.map((l) => (
            <tr key={l.position} className="border-b last:border-0">
              <td className="px-3 py-2">{l.description}</td>
              <td className="px-3 py-2 text-right tabular-nums">{toNumber(l.quantity)}</td>
              <td className="px-3 py-2 text-right tabular-nums">{formatMoney(l.unit_price)}</td>
              <td className="px-3 py-2 text-right tabular-nums">{formatMoney(l.total)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
