import { useAuth } from "@/features/auth/auth";
import { useGet } from "@/hooks/useApi";
import { today } from "@/lib/formatters";
import type { UserLite } from "@/types/services";

/** Turns `business_card` into `Business card`. */
export function label(value: string | null | undefined) {
  if (!value) return "-";
  const text = value.replaceAll("_", " ");
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** Staff the signed-in user may assign work to. Empty when they cannot list users. */
export function useStaff() {
  const { can } = useAuth();
  const users = useGet<UserLite[]>(
    "/users",
    { per_page: 100 },
    { enabled: can("security.user.manage") },
  );
  return (users.data ?? []).filter((u) => u.is_active);
}

/** Days from today to an ISO date. Negative when the date has passed. */
export function daysUntil(date: string | null | undefined) {
  if (!date) return null;
  const a = new Date(`${today()}T00:00:00`);
  const b = new Date(`${date.slice(0, 10)}T00:00:00`);
  return Math.round((b.getTime() - a.getTime()) / 86_400_000);
}

export const COST_KINDS = [
  "government_fee",
  "contractor",
  "materials",
  "printing",
  "stock_images",
  "fonts",
  "designer_time",
  "other",
];

/** Builds a WhatsApp click-to-chat URL (`wa.me`) prefilled with a message. */
export function whatsappUrl(phone: string | null | undefined, text: string) {
  const digits = (phone ?? "").replace(/\D/g, "");
  const encoded = encodeURIComponent(text);
  return digits ? `https://wa.me/${digits}?text=${encoded}` : `https://wa.me/?text=${encoded}`;
}

export const clientBlank = {
  name: "",
  phone: "",
  email: "",
  address: "",
  id_number: "",
  source: "",
  needs: "",
  notes: "",
  status: "lead",
};
