import { SegmentedChips } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useI18n } from "@/features/i18n/i18n";
import { cn } from "@/lib/utils";
import type { BusinessChoice } from "@/types";
import { useBusiness } from "./useBusiness";

/** Chooses which business the shared pages show. Hidden from people who have only one. */
export function BusinessSwitcher({ className }: { className?: string }) {
  const { t } = useI18n();
  const { units, selected, setBusiness } = useBusiness();
  const { me, setBranchId } = useAuth();
  if (units.length < 2) return null;
  const choose = (choice: BusinessChoice) => {
    setBusiness(choice);
    // A branch belongs to one business, so showing both means looking at every branch.
    if (choice === "all" && me?.is_owner) setBranchId("all");
  };
  return (
    <SegmentedChips
      ariaLabel={t.business.switcher}
      className={cn("flex-nowrap whitespace-nowrap", className)}
      value={selected}
      onChange={choose}
      options={[
        { value: "A", label: t.common.shop },
        { value: "B", label: t.common.services },
        { value: "all", label: t.common.all },
      ]}
    />
  );
}
