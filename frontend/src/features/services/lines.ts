import { toNumber } from "@/lib/formatters";
import type { DocLine } from "@/types/services";

export interface LineDraft {
  service_id: string;
  description: string;
  quantity: string;
  unit_price: string;
}

export const blankLine: LineDraft = {
  service_id: "",
  description: "",
  quantity: "1",
  unit_price: "",
};

export function linesFrom(lines: DocLine[] | undefined): LineDraft[] {
  return (lines ?? []).map((l) => ({
    service_id: l.service_id ?? "",
    description: l.description,
    quantity: String(toNumber(l.quantity)),
    unit_price: l.unit_price,
  }));
}

/** What the API expects for each line. Blank price lets the API use the catalog price. */
export function linesBody(lines: LineDraft[]) {
  return lines
    .filter((l) => l.description.trim() || l.service_id)
    .map((l) => ({
      service_id: l.service_id || undefined,
      description: l.description,
      quantity: l.quantity || "1",
      unit_price: l.unit_price || undefined,
    }));
}

export function linesTotal(lines: LineDraft[]) {
  return lines.reduce((sum, l) => sum + toNumber(l.quantity) * toNumber(l.unit_price), 0);
}
