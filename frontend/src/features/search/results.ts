import { isDocumentNumber } from "./query";
import type { RecordKind, SearchHit, SearchResult } from "./types";

export const PER_GROUP = 5;

/** Kinds that carry a document number. */
export const DOCUMENT_KINDS: RecordKind[] = [
  "sale",
  "service_invoice",
  "quotation",
  "project",
  "design_job",
];

/** True when the hit is the very document or barcode that was typed. */
export function isExactMatch(hit: SearchHit, text: string) {
  const wanted = text.trim().toLowerCase();
  if (!wanted) return false;
  if (DOCUMENT_KINDS.includes(hit.kind) && isDocumentNumber(wanted))
    // Titles read "R-0001" or "PRJ-0001 · Logo".
    return hit.title.split(" · ")[0].toLowerCase() === wanted;
  if (hit.kind !== "product") return false;
  return [hit.meta?.barcode, hit.meta?.sku, hit.subtitle].some((v) => v?.toLowerCase() === wanted);
}

/** The reply of `GET /search`, with an empty result when there is nothing in it. */
export function toSearchResult(reply: Partial<SearchResult> | null | undefined): SearchResult {
  return { best: reply?.best ?? null, groups: reply?.groups ?? [] };
}

/** The list page that shows every match of one kind. */
export function listLink(kind: RecordKind, text: string) {
  const pages: Record<RecordKind, string> = {
    product: "/products",
    customer: "/customers",
    client: "/clients",
    sale: "/sales",
    service_invoice: "/service-invoices",
    quotation: "/service-quotations",
    project: "/projects",
    design_job: "/design-jobs",
  };
  return `${pages[kind]}?q=${encodeURIComponent(text)}`;
}
