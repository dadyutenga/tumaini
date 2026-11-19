/** Shapes returned by the Services (Business B) API. Money arrives as strings. */

export interface Client {
  id: string;
  name: string;
  phone: string | null;
  email: string | null;
  address: string | null;
  id_number: string | null;
  source: string | null;
  needs: string | null;
  notes: string | null;
  status: "lead" | "active" | "completed";
  is_active: boolean;
  balance: string;
  deposit_held: string;
  projects: number;
  created_at: string;
}

export interface ClientDetail extends Client {
  projects_list: {
    id: string;
    number: string;
    name: string;
    status: string;
    total_price: string;
    paid: string;
  }[];
  invoices: {
    id: string;
    number: string;
    status: string;
    issue_date: string | null;
    due_date: string | null;
    total: string;
    paid_total: string;
    balance: string;
  }[];
  payments: {
    id: string;
    number: string;
    date: string;
    method: string;
    amount: string;
    applied: string;
    status: string;
  }[];
  documents: ClientDocument[];
  communication: {
    id: string;
    kind: string;
    body: string;
    noted_on: string;
    project_id: string | null;
    project_number: string | null;
  }[];
}

export interface CatalogItem {
  id: string;
  name: string;
  category: string;
  description: string | null;
  standard_price: string;
  government_fees: string;
  included_revisions: number;
  is_package: boolean;
  is_active: boolean;
  stage_count: number;
  service_count: number;
}

export interface CatalogDetail extends CatalogItem {
  stages: { position: number; name: string; est_days: number }[];
  documents: { id: string; name: string; stage_position: number | null }[];
  package_services: { id: string; name: string }[] | string[];
}

export interface ProjectRow {
  id: string;
  number: string;
  name: string;
  client_id: string;
  client_name: string;
  service_id: string | null;
  owner_id: string | null;
  owner_name: string | null;
  status: "in_progress" | "on_hold" | "completed" | "cancelled";
  current_stage: string | null;
  stages_done: number;
  stages_total: number;
  start_date: string;
  total_price: string;
  paid: string;
  balance: string;
  costs: string;
  profit: string;
  notes: string | null;
  completed_at: string | null;
  stage_status?: string;
  stage_deadline?: string | null;
}

export interface Stage {
  id: string;
  position: number;
  name: string;
  status: "not_started" | "in_progress" | "done" | "stuck";
  owner_id: string | null;
  owner_name: string | null;
  deadline: string | null;
  note: string | null;
  checklist: { text: string; done: boolean }[];
  documents_missing: number;
  est_days: number;
  completed_at: string | null;
}

export interface ClientDocument {
  id: string;
  name: string;
  doc_type: string;
  status: "missing" | "received";
  file_id: string | null;
  file_name?: string | null;
  mime_type?: string | null;
  received_at: string | null;
  client_id?: string;
  client_name?: string;
  project_id?: string | null;
  project_number?: string | null;
  stage_id?: string | null;
  design_job_id?: string | null;
  created_at?: string;
}

export interface Cost {
  id: string;
  number: string;
  kind: string;
  description: string;
  amount: string;
  date: string;
  status: "paid" | "owed" | "void";
  settled: string;
  contractor_id: string | null;
  contractor_name: string | null;
  project_id: string | null;
  design_job_id: string | null;
}

export interface ServicePayment {
  id: string;
  number: string;
  date: string;
  method: string;
  amount: string;
  applied: string;
  status: string;
  reference?: string | null;
  client_id?: string;
  client_name?: string;
  project_id?: string | null;
  design_job_id?: string | null;
}

export interface ProjectNote {
  id: string;
  kind: string;
  body: string;
  noted_on: string;
  created_at: string;
  author?: string;
}

export interface StageEvent {
  id: string;
  stage_id: string;
  stage_name: string;
  from_status: string | null;
  to_status: string;
  note: string | null;
  created_at: string;
  actor_name: string | null;
}

export interface ProjectDetail extends ProjectRow {
  stages: Stage[];
  documents: ClientDocument[];
  payments: ServicePayment[];
  costs_list: Cost[];
  notes_list: ProjectNote[];
  invoices: InvoiceRow[];
  design_jobs: DesignJobRow[];
  stage_events?: StageEvent[];
}

export interface PrintOrderRow {
  id: string;
  job_id: string;
  job_number: string;
  job_title: string;
  client_id: string;
  client_name: string;
  contractor_id: string | null;
  contractor_name: string | null;
  quantity: number;
  cost: string;
  due_date: string | null;
  status: "sent" | "ready" | "delivered" | "cancelled";
  note: string | null;
  created_at: string;
}

export interface DocLine {
  service_id: string | null;
  description: string;
  quantity: string;
  unit_price: string;
  total: string;
  position: number;
}

export interface Quotation {
  id: string;
  number: string;
  client_id: string;
  client_name: string;
  project_id: string | null;
  invoice_id: string | null;
  status: "draft" | "sent" | "accepted" | "rejected";
  valid_until: string | null;
  subtotal: string;
  discount: string;
  total: string;
  notes: string | null;
  created_at: string;
  lines?: DocLine[];
}

export interface InvoiceRow {
  id: string;
  number: string;
  client_id: string;
  client_name: string;
  project_id: string | null;
  project_number: string | null;
  design_job_id: string | null;
  quotation_id: string | null;
  status: "draft" | "issued" | "partial" | "paid" | "void";
  issue_date: string | null;
  due_date: string | null;
  subtotal: string;
  discount: string;
  total: string;
  paid_total: string;
  balance: string;
  void_reason: string | null;
  notes: string | null;
}

export interface InvoiceDetail extends InvoiceRow {
  lines: DocLine[];
  installments: { id?: string; due_date: string; amount: string; paid?: string; status?: string }[];
  payments: ServicePayment[];
}

export interface Task {
  id: string;
  title: string;
  description: string | null;
  status: "todo" | "in_progress" | "done" | "cancelled";
  priority: "normal" | "urgent";
  due_date: string | null;
  overdue: boolean;
  assignee_id: string | null;
  assignee_name: string | null;
  project_id: string | null;
  project_number: string | null;
  design_job_id: string | null;
  completed_at: string | null;
}

export interface Renewal {
  id: string;
  client_id: string;
  client_name: string;
  client_phone: string | null;
  project_id: string | null;
  item: string;
  due_date: string;
  days_left: number;
  amount: string | null;
  notes: string | null;
  status: "pending" | "renewed" | "cancelled";
  reminded_30_at: string | null;
  reminded_7_at: string | null;
}

export interface Contractor {
  id: string;
  name: string;
  trade: string | null;
  phone: string | null;
  email: string | null;
  address: string | null;
  notes: string | null;
  is_active: boolean;
  owed: string;
}

export interface ContractorStatement {
  contractor: Contractor;
  owed: string;
  lines: {
    date: string;
    number: string;
    description: string;
    owed_amount: string;
    paid_amount: string;
    balance: string;
  }[];
}

export const DESIGN_STATUSES = [
  "new_brief",
  "in_design",
  "internal_review",
  "with_client",
  "changes_requested",
  "approved",
  "printing",
  "delivered",
] as const;
export type DesignStatus = (typeof DESIGN_STATUSES)[number] | "cancelled";

export const DESIGN_NEXT: Record<string, DesignStatus[]> = {
  new_brief: ["in_design", "cancelled"],
  in_design: ["internal_review", "with_client", "cancelled"],
  internal_review: ["in_design", "with_client", "cancelled"],
  with_client: ["in_design", "changes_requested", "approved", "cancelled"],
  changes_requested: ["in_design", "cancelled"],
  approved: ["printing", "delivered"],
  printing: ["delivered"],
};

export const JOB_TYPES = [
  "logo",
  "business_card",
  "poster",
  "flyer",
  "banner",
  "signage",
  "social_media",
  "packaging_label",
  "branding_package",
  "other",
] as const;

export interface DesignJobRow {
  id: string;
  number: string;
  title: string;
  job_type: string;
  status: DesignStatus;
  priority: "normal" | "urgent";
  client_id: string;
  client_name: string;
  project_id: string | null;
  project_number: string | null;
  stage_id: string | null;
  designer_id: string | null;
  designer_name: string | null;
  deadline: string | null;
  overdue: boolean;
  revisions_included: number;
  revisions_used: number;
  revisions_over: boolean;
  printing_needed: boolean;
  price: string;
  deposit: string;
  paid: string;
  balance: string;
  costs: string;
  profit: string;
}

export interface DesignVersion {
  id: string;
  version_no: number;
  kind: "design" | "print_ready" | "source";
  status: "draft" | "sent" | "changes_requested" | "approved" | string;
  is_final: boolean;
  file_id: string | null;
  file_name: string | null;
  mime_type: string | null;
  link: string | null;
  notes: string | null;
  uploaded_by: string | null;
  created_at: string;
  token_expires_at: string | null;
  decided_at: string | null;
}

export interface PrintOrder {
  id: string;
  contractor_name?: string | null;
  quantity: number;
  cost: string;
  due_date: string | null;
  status: string;
  note?: string | null;
}

export interface DesignJobDetail extends DesignJobRow {
  brief: string | null;
  size_format: string | null;
  style_notes: string | null;
  text_to_include: string | null;
  service_id: string | null;
  versions: DesignVersion[];
  feedback: {
    id?: string;
    version_id: string | null;
    version_no?: number | null;
    author_type: string;
    author_name?: string | null;
    comment: string;
    created_at: string;
  }[];
  references: { id?: string; file_id: string; file_name?: string | null }[];
  costs_list: Cost[];
  payments: ServicePayment[];
  print_orders: PrintOrder[];
  time: {
    id?: string;
    user_name?: string | null;
    started_at: string;
    ended_at: string | null;
    minutes?: number | null;
  }[];
  invoices: InvoiceRow[];
}

export interface PublicApproval {
  title: string;
  job_type: string;
  client_name: string;
  business_name: string;
  revisions_included: number;
  revisions_used: number;
  version_no: number;
  status: string;
  expires_at: string;
  decided_at: string | null;
  has_file: boolean;
  file_name?: string;
  mime_type?: string;
  link: string | null;
  comments: { author_type: string; comment: string; created_at: string }[];
}

export interface ServicesDashboard {
  debts?: { overdue: string; overdue_invoices: number; owed_to_us: string };
  design?: {
    changes_requested: number;
    due_today: number;
    in_progress: number;
    overdue: number;
    waiting_client: number;
  };
  late_projects?: number;
  overdue_task_count?: number;
  overdue_tasks?: { id: string; title: string; due_date: string; assignee_name: string | null }[];
  projects_by_stage?: { stage: string; projects: number }[];
  renewals_due?: Renewal[];
  timer_job_id?: string | null;
  we_owe_contractors?: string;
}

export interface UserLite {
  id: string;
  name: string;
  is_active: boolean;
}
