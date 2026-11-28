import type { RecordKind } from "./types";

export type Scope =
  | "all"
  | "products"
  | "customers"
  | "clients"
  | "sales"
  | "invoices"
  | "projects"
  | "design"
  | "pages";

export interface ScopeInfo {
  id: Scope;
  /** The record kinds shown in this scope. */
  kinds: RecordKind[];
  /** Any one of these permissions shows the chip. Empty means everyone. */
  permissions: string[];
  /** The business the records belong to. */
  business?: "A" | "B";
}

/** The chips under the search box, with the same permissions the server checks. */
export const SCOPES: ScopeInfo[] = [
  { id: "all", kinds: [], permissions: [] },
  { id: "products", business: "A", kinds: ["product"], permissions: ["shop.product.view"] },
  {
    id: "customers",
    business: "A",
    kinds: ["customer"],
    permissions: ["shop.customer.view_all", "shop.customer.manage"],
  },
  {
    id: "clients",
    business: "B",
    kinds: ["client"],
    permissions: ["services.client.view_all", "services.client.manage"],
  },
  {
    id: "sales",
    business: "A",
    kinds: ["sale"],
    permissions: ["shop.sale.view_all", "shop.sale.create"],
  },
  {
    id: "invoices",
    business: "B",
    kinds: ["service_invoice", "quotation"],
    permissions: [
      "services.invoice.manage",
      "services.invoice.draft",
      "services.quotation.manage",
      "services.quotation.draft",
    ],
  },
  {
    id: "projects",
    business: "B",
    kinds: ["project"],
    permissions: ["services.project.view_all", "services.project.manage"],
  },
  {
    id: "design",
    business: "B",
    kinds: ["design_job"],
    permissions: ["services.design.view_all", "services.design.manage"],
  },
  { id: "pages", kinds: [], permissions: [] },
];
