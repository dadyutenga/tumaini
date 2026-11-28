import { useAuth } from "@/features/auth/auth";
import { useI18n, type Translations } from "@/features/i18n/i18n";
import en from "@/features/i18n/locales/en";

export interface QuickAction {
  id: string;
  label: string;
  /** The English name, so an action is found whatever the language of the screen. */
  keywords: string;
  to: string;
}

/**
 * The things a person can start from anywhere. The `+ New` menu and the search palette both
 * use this list, so permissions are decided in one place.
 */
export function useQuickActions() {
  const { can, canAny } = useAuth();
  const { t } = useI18n();
  const action = (
    id: string,
    name: (words: Translations) => string,
    to: string,
    show: boolean,
  ) => ({ id, label: name(t), keywords: name(en), to, show });

  const create = [
    action("sale", (w) => w.common.newSalePos, "/pos", can("shop.sale.create")),
    action("client", (w) => w.common.newClient, "/clients", can("services.client.manage")),
    action("project", (w) => w.common.newProject, "/projects", can("services.project.manage")),
    action("design", (w) => w.common.newDesignJob, "/design-jobs", can("services.design.manage")),
    action(
      "receive",
      (w) => w.common.receivePayment,
      "/service-invoices",
      canAny("services.invoice.manage", "finance.payment.receive"),
    ),
    action(
      "customer-payment",
      (w) => w.common.customerPayment,
      "/customers",
      canAny("shop.customer.view_all", "shop.customer.manage", "finance.payment.receive"),
    ),
    action(
      "expense",
      (w) => w.common.newExpense,
      "/expenses",
      canAny("finance.expense.submit", "finance.expense.enter"),
    ),
    action("task", (w) => w.common.newTask, "/service-tasks", can("services.task.manage")),
  ].filter((a) => a.show);
  // Clocking in and out happens on the home screen, so search only takes the user there.
  const clock = action("clock", (w) => w.search.clockInOut, "/", true);
  return { create, all: [...create, clock] };
}
