import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Field, FormDialog, NativeSelect } from "@/components/shared/kit";
import { ClientSelect, StaffSelect } from "@/features/services/shared";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatMoney, today } from "@/lib/formatters";
import type { CatalogItem, ProjectRow } from "@/types/services";

/** Starts a project. Stages and required documents come from the chosen service. */
export function NewProjectDialog({
  open,
  onOpenChange,
  clientId = "",
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clientId?: string;
}) {
  const navigate = useNavigate();
  const catalog = useGet<CatalogItem[]>("/service-catalog", { per_page: 100 });
  const [client, setClient] = useState(clientId);
  const [serviceId, setServiceId] = useState("");
  const [name, setName] = useState("");
  const [price, setPrice] = useState("");
  const [owner, setOwner] = useState("");
  const [start, setStart] = useState(today());
  const [notes, setNotes] = useState("");
  const service = catalog.data?.find((s) => s.id === serviceId);
  const create = useWrite<unknown, ProjectRow>("POST", "/projects", {
    invalidate: ["/projects", "/clients", "/services"],
    success: "Project started",
    onSuccess: (p) => {
      onOpenChange(false);
      navigate(`/projects/${p.id}`);
    },
  });

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New project"
      description="Stages and required documents are created from the service you pick."
      busy={create.isPending}
      onSubmit={() =>
        create.mutate({
          client_id: client || clientId,
          service_id: serviceId || undefined,
          name: name || undefined,
          start_date: start,
          owner_id: owner || undefined,
          total_price: price || undefined,
          notes: notes || undefined,
        })
      }
      wide
    >
      <div className="grid gap-3 sm:grid-cols-2">
        {!clientId && (
          <Field label="Client *" className="sm:col-span-2">
            <ClientSelect required value={client} onChange={setClient} />
          </Field>
        )}
        <Field label="Service or package" className="sm:col-span-2">
          <NativeSelect value={serviceId} onChange={(e) => setServiceId(e.target.value)}>
            <option value="">Custom project, no stages</option>
            {catalog.data
              ?.filter((s) => s.is_active)
              .map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name} · {formatMoney(s.standard_price)} · {s.stage_count} stages
                </option>
              ))}
          </NativeSelect>
        </Field>
        <Field label="Project name">
          <Input
            placeholder={service ? `${service.name} - client` : "Required for a custom project"}
            required={!serviceId}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label="Total price">
          <Input
            inputMode="decimal"
            placeholder={service?.standard_price}
            value={price}
            onChange={(e) => setPrice(e.target.value)}
          />
        </Field>
        <Field label="Start date">
          <Input type="date" value={start} onChange={(e) => setStart(e.target.value)} />
        </Field>
        <Field label="Owner">
          <StaffSelect value={owner} onChange={setOwner} empty="Me" />
        </Field>
        <Field label="Notes" className="sm:col-span-2">
          <Textarea rows={2} value={notes} onChange={(e) => setNotes(e.target.value)} />
        </Field>
      </div>
    </FormDialog>
  );
}
