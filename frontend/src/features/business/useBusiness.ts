import { useAuth } from "@/features/auth/auth";
import type { BusinessChoice, BusinessCode } from "@/types";

export const BUSINESSES: BusinessCode[] = ["A", "B"];

/** The businesses the signed-in person works in, and the one the switcher is showing. */
export function useBusiness() {
  const { me, business, setBusiness } = useAuth();
  const units = me?.business_units?.length ? me.business_units : BUSINESSES;
  // Someone with one business is always in it.
  const selected: BusinessChoice = units.length === 1 ? units[0] : (business ?? "all");
  /** The person may use things of this business; things of neither are for everyone. */
  const works = (code?: BusinessCode | null) => !code || units.includes(code);
  /** Part of what is on screen now: allowed, and not left out by the switcher. */
  const shows = (code?: BusinessCode | null) =>
    works(code) && (!code || selected === "all" || selected === code);
  return { units, selected, works, shows, setBusiness };
}
