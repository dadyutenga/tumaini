import { useState } from "react";
import { Input } from "@/components/ui/input";
import { DataGrid, PageHeader } from "@/components/shared/kit";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";

interface Entry {
  id: string;
  action: string;
  entity_type: string;
  entity_id: string;
  user_id: string;
  created_at: string;
}

export default function Audit() {
  const [action, setAction] = useState("");
  const debounced = useDebounce(action, 300);
  const { data, isLoading } = useGet<Entry[]>("/audit-logs", { action: debounced });
  return (
    <>
      <PageHeader
        title="Activity log"
        description="Who did what, and when. Entries cannot be edited."
      />
      <Input
        aria-label="Filter by action"
        className="mb-4 max-w-xs"
        placeholder="Filter by action, for example sale.create"
        value={action}
        onChange={(e) => setAction(e.target.value)}
      />
      <DataGrid
        rows={data}
        loading={isLoading}
        rowKey={(e) => e.id}
        empty="No activity recorded"
        columns={[
          { header: "When", cell: (e) => formatDateTime(e.created_at) },
          { header: "Action", cell: (e) => <code className="text-xs">{e.action}</code> },
          { header: "Record", cell: (e) => e.entity_type },
          {
            header: "Record ID",
            cell: (e) => <code className="text-xs">{e.entity_id?.slice(0, 8)}</code>,
          },
          {
            header: "User",
            cell: (e) => <code className="text-xs">{e.user_id?.slice(0, 8)}</code>,
          },
        ]}
      />
    </>
  );
}
