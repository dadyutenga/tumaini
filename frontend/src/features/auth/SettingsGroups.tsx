import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";

type Value = string | number | boolean;
type Group = Record<string, Value>;
type Settings = Record<string, Record<string, unknown>>;

const titles: Record<string, string> = {
  receipt: "Receipts",
  invoice: "Invoices",
  payroll: "Payroll",
  notifications: "Reminders",
  system: "System",
  tax: "Tax",
  currency: "Currency",
};
const order = ["receipt", "invoice", "tax", "currency", "payroll", "notifications", "system"];

const label = (key: string) => {
  const text = key.replaceAll("_", " ");
  return text.charAt(0).toUpperCase() + text.slice(1);
};
const isScalar = (v: unknown): v is Value =>
  typeof v === "string" || typeof v === "number" || typeof v === "boolean";

function GroupForm({ name, initial, canEdit }: { name: string; initial: Group; canEdit: boolean }) {
  const [draft, setDraft] = useState<Group>(initial);
  useEffect(() => setDraft(initial), [initial]);
  const save = useWrite<Group, unknown>("PUT", `/settings/${name}`, {
    invalidate: ["/settings", "/me"],
    success: `${titles[name] ?? name} saved`,
  });
  const entries = Object.entries(initial);
  if (entries.length === 0) return null;
  return (
    <form
      className="rounded-2xl border bg-card p-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(draft);
      }}
    >
      <h3 className="mb-3 font-semibold">{titles[name] ?? label(name)}</h3>
      <div className="grid gap-3 sm:grid-cols-2">
        {entries.map(([key, original]) => (
          <label key={key} className="flex flex-col gap-1 text-sm">
            {typeof original === "boolean" ? (
              <span className="flex items-center gap-2">
                <input
                  type="checkbox"
                  disabled={!canEdit}
                  checked={Boolean(draft[key])}
                  onChange={(e) => setDraft((d) => ({ ...d, [key]: e.target.checked }))}
                />
                {label(key)}
              </span>
            ) : (
              <>
                <span className="text-muted-foreground">{label(key)}</span>
                <Input
                  disabled={!canEdit}
                  inputMode={typeof original === "number" ? "decimal" : undefined}
                  value={String(draft[key] ?? "")}
                  onChange={(e) =>
                    setDraft((d) => ({
                      ...d,
                      [key]:
                        typeof original === "number" ? Number(e.target.value) || 0 : e.target.value,
                    }))
                  }
                />
              </>
            )}
          </label>
        ))}
      </div>
      {canEdit && (
        <Button type="submit" className="mt-4" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
      )}
    </form>
  );
}

/** Every settings group as a simple form. Nested values (such as payment methods) have their own screens. */
export function SettingsGroups() {
  const { can } = useAuth();
  const settings = useGet<Settings>("/settings");
  if (!settings.data) return null;
  const groups = order.filter((g) => settings.data[g]);
  return (
    <div className="grid max-w-4xl gap-4">
      {groups.map((g) => {
        const flat = Object.fromEntries(
          Object.entries(settings.data[g]).filter(([, v]) => isScalar(v)),
        ) as Group;
        return <GroupForm key={g} name={g} initial={flat} canEdit={can("settings.manage")} />;
      })}
    </div>
  );
}
