import { Field, NativeSelect } from "@/components/shared/kit";
import { useI18n } from "@/features/i18n/i18n";
import { useBusiness } from "./useBusiness";

/**
 * The business a money record belongs to, for people who work in both. `value` is undefined until
 * chosen, and then follows the switcher; "" leaves it to the branch.
 */
export function useBusinessChoice(value: string | undefined) {
  const { selected } = useBusiness();
  return value ?? (selected === "all" ? "" : selected);
}

export function BusinessSelect({
  value,
  onChange,
}: {
  value: string | undefined;
  onChange: (next: string) => void;
}) {
  const { t } = useI18n();
  const { units } = useBusiness();
  const shown = useBusinessChoice(value);
  if (units.length < 2) return null;
  return (
    <Field label={t.business.business}>
      <NativeSelect value={shown} onChange={(e) => onChange(e.target.value)}>
        <option value="">{t.business.branchDefault}</option>
        <option value="A">{t.business.shopA}</option>
        <option value="B">{t.business.servicesB}</option>
      </NativeSelect>
    </Field>
  );
}
