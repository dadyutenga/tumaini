import {
  Briefcase,
  FileSpreadsheet,
  FileText,
  FolderKanban,
  Package,
  Palette,
  Receipt,
  User,
} from "lucide-react";
import type { RecordKind } from "./types";

export const KIND_ICONS: Record<RecordKind, typeof Package> = {
  product: Package,
  customer: User,
  client: Briefcase,
  sale: Receipt,
  service_invoice: FileText,
  quotation: FileSpreadsheet,
  project: FolderKanban,
  design_job: Palette,
};
