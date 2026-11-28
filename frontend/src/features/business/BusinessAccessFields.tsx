import { Button } from "@/components/ui/button";
import { useI18n } from "@/features/i18n/i18n";
import type { Branch, BusinessCode } from "@/types";
import { useBusiness } from "./useBusiness";

export interface BusinessAccess {
  business_units: BusinessCode[];
  primary_business_unit: BusinessCode;
  /** Branches the person is limited to; empty allows every branch of their businesses. */
  locations: string[];
}

/** The branches left after a change of businesses. */
const within = (locations: string[], branches: Branch[], units: BusinessCode[]) =>
  locations.filter((id) => {
    const unit = branches.find((b) => b.id === id)?.business_unit;
    return !unit || units.includes(unit);
  });

/** Access with exactly these businesses, keeping the rest of it consistent. */
export function withBusinesses(
  value: BusinessAccess,
  units: BusinessCode[],
  branches: Branch[],
): BusinessAccess {
  const sorted = [...units].sort();
  return {
    business_units: sorted,
    primary_business_unit: sorted.includes(value.primary_business_unit)
      ? value.primary_business_unit
      : (sorted[0] ?? "A"),
    locations: within(value.locations, branches, sorted),
  };
}

/**
 * Which businesses a person works in, which is their main one and, optionally, which branches they
 * are limited to. An Admin always has both businesses.
 */
export function BusinessAccessFields({
  value,
  onChange,
  branches,
  admin = false,
  onPreset,
}: {
  value: BusinessAccess;
  onChange: (next: BusinessAccess) => void;
  branches: Branch[];
  admin?: boolean;
  /** Offers the quick set-ups; the caller also gives the person the Staff role. */
  onPreset?: (unit: BusinessCode) => void;
}) {
  const { t } = useI18n();
  // Nobody can hand out a business they do not work in.
  const { units: offered, works } = useBusiness();
  const name = (code: BusinessCode) => (code === "A" ? t.business.shopA : t.business.servicesB);
  const chosen = admin ? offered : value.business_units;
  const choices = branches.filter(
    (b) => b.is_active && (!b.business_unit || chosen.includes(b.business_unit)),
  );
  const hint = admin
    ? t.business.adminHasBoth
    : chosen.length === 0
      ? t.business.chooseOne
      : chosen.length > 1
        ? t.business.seesBoth
        : chosen[0] === "A"
          ? t.business.seesShop
          : t.business.seesServices;
  const toggle = (code: BusinessCode, on: boolean) =>
    onChange(
      withBusinesses(
        value,
        on ? [...value.business_units, code] : value.business_units.filter((u) => u !== code),
        branches,
      ),
    );

  return (
    <fieldset className="space-y-3 rounded-xl border border-[var(--od-border-strong)] p-3">
      <legend className="px-1 text-sm font-semibold text-[var(--od-ink)]">
        {t.business.access}
      </legend>
      {onPreset && !admin && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="od-caption">{t.business.quickSetUp}</span>
          {works("A") && (
            <Button type="button" size="sm" variant="outline" onClick={() => onPreset("A")}>
              {t.business.shopSalesperson}
            </Button>
          )}
          {works("B") && (
            <Button type="button" size="sm" variant="outline" onClick={() => onPreset("B")}>
              {t.business.servicesOfficer}
            </Button>
          )}
        </div>
      )}
      <div className="grid gap-2 sm:grid-cols-2">
        {offered.map((code) => {
          const ticked = chosen.includes(code);
          return (
            <div key={code} className="space-y-1">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={ticked}
                  disabled={admin}
                  onChange={(e) => toggle(code, e.target.checked)}
                />
                {name(code)}
              </label>
              <label className="flex items-center gap-2 pl-6 text-xs text-[var(--od-ink-secondary)]">
                <input
                  type="radio"
                  name="primary-business"
                  aria-label={`${t.business.primary}: ${name(code)}`}
                  checked={ticked && value.primary_business_unit === code}
                  disabled={!ticked}
                  onChange={() => onChange({ ...value, primary_business_unit: code })}
                />
                {t.business.primary}
              </label>
            </div>
          );
        })}
      </div>
      <p className="od-caption" role="status">
        {hint}
      </p>
      {!admin && choices.length > 1 && (
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-[var(--od-ink-secondary)]">
            {t.business.branches}
          </p>
          <div className="flex flex-wrap gap-x-4 gap-y-1.5">
            {choices.map((b) => (
              <label key={b.id} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={value.locations.includes(b.id)}
                  onChange={(e) =>
                    onChange({
                      ...value,
                      locations: e.target.checked
                        ? [...value.locations, b.id]
                        : value.locations.filter((id) => id !== b.id),
                    })
                  }
                />
                {b.name}
              </label>
            ))}
          </div>
          <p className="od-caption">{t.business.branchesHint}</p>
        </div>
      )}
    </fieldset>
  );
}
