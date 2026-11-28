import { Badge } from "@/components/ui/badge";
import { useI18n } from "@/features/i18n/i18n";
import type { BusinessCode } from "@/types";

export function useBusinessName() {
  const { t } = useI18n();
  return (code: BusinessCode) => (code === "A" ? t.common.shop : t.common.services);
}

/** The businesses of a person or a record, as badges. */
export function BusinessBadges({ units }: { units?: (BusinessCode | undefined | null)[] }) {
  const name = useBusinessName();
  const known = (units ?? []).filter((u): u is BusinessCode => u === "A" || u === "B");
  if (!known.length) return <span>-</span>;
  return (
    <span className="flex flex-wrap gap-1">
      {known.map((u) => (
        <Badge key={u} variant="outline">
          {name(u)}
        </Badge>
      ))}
    </span>
  );
}
