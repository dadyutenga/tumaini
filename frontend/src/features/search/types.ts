/** The kinds of record the search finds, in the order their groups are listed. */
export const RECORD_KINDS = [
  "product",
  "customer",
  "client",
  "sale",
  "service_invoice",
  "quotation",
  "project",
  "design_job",
] as const;
export type RecordKind = (typeof RECORD_KINDS)[number];

/** Extra fields for the right-hand side of a row. Which ones are present depends on the kind. */
export interface HitMeta {
  sku?: string | null;
  barcode?: string | null;
  price?: string | null;
  stock?: string | null;
  low_stock?: boolean;
  phone?: string | null;
  balance?: string | null;
  total?: string | null;
  date?: string | null;
  status?: string | null;
  due_date?: string | null;
  late?: boolean;
  client?: string | null;
}

export interface SearchHit {
  kind: RecordKind;
  id: string;
  title: string;
  subtitle: string;
  link: string;
  meta?: HitMeta;
  business_unit?: "A" | "B" | null;
  highlight?: string | null;
}

export interface SearchGroup {
  kind: RecordKind;
  total: number;
  hits: SearchHit[];
  /** The server ran out of time before this group finished. */
  partial?: boolean;
}

export interface SearchResult {
  best: SearchHit | null;
  groups: SearchGroup[];
}
