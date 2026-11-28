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
}

/** The chips under the search box, with the same permissions the server checks. */
export const SCOPES: ScopeInfo[] = [
  { id: "all", kinds: [], permissions: [] },
  { id: "products", kinds: ["product"], permissions: ["shop.product.view"] },
  {
    id: "customers",
    kinds: ["customer"],
    permissions: ["shop.customer.view_all", "shop.customer.manage"],
  },
  {
    id: "clients",
    kinds: ["client"],
    permissions: ["services.client.view_all", "services.client.manage"],
  },
  { id: "sales", kinds: ["sale"], permissions: ["shop.sale.view_all", "shop.sale.create"] },
  {
    id: "invoices",
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
    kinds: ["project"],
    permissions: ["services.project.view_all", "services.project.manage"],
  },
  {
    id: "design",
    kinds: ["design_job"],
    permissions: ["services.design.view_all", "services.design.manage"],
  },
  { id: "pages", kinds: [], permissions: [] },
];
