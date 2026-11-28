import { isDocumentNumber } from "./query";
import { RECORD_KINDS, type RecordKind, type SearchHit, type SearchResult } from "./types";

export const PER_GROUP = 5;

/** Kinds that carry a document number. */
export const DOCUMENT_KINDS: RecordKind[] = [
  "sale",
  "service_invoice",
  "quotation",
  "project",
  "design_job",
];

const isKind = (value: string): value is RecordKind =>
  (RECORD_KINDS as readonly string[]).includes(value);

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

/**
 * Reads either reply of `GET /search`: the grouped one, or the flat list the server sends
 * today. TODO(search-v2): drop the flat list once the grouped response is deployed.
 */
export function toSearchResult(data: unknown, text: string): SearchResult {
  if (!Array.isArray(data)) {
    const result = data as Partial<SearchResult> | null | undefined;
    return { best: result?.best ?? null, groups: result?.groups ?? [] };
  }
  const hits = (data as SearchHit[])
    .filter((hit) => isKind(hit.kind))
    .map((hit) =>
      // The flat reply links a product to the list; the record itself is more useful.
      hit.kind === "product" ? { ...hit, link: `/products/${hit.id}` } : hit,
    );
  const best = hits.find((hit) => isExactMatch(hit, text)) ?? null;
  const groups = RECORD_KINDS.map((kind) => {
    const rows = hits.filter((hit) => hit.kind === kind);
    return { kind, total: rows.length, hits: rows };
  }).filter((group) => group.total > 0);
  return { best, groups };
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
