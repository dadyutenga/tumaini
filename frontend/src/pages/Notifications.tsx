import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowUpRight, Check, CheckCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { FilterBar, PageHeader, Pager, SegmentedChips } from "@/components/shared/kit";
import { EmptyState, PageLoader } from "@/components/shared/Display";
import { useGetPage, useWrite } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";
import { cn } from "@/lib/utils";
import { useI18n } from "@/features/i18n/i18n";

interface Notice {
  id: string;
  type?: string;
  title?: string;
  body?: string;
  message?: string;
  link?: string | null;
  is_read?: boolean;
  read_at?: string | null;
  created_at?: string;
}

export default function Notifications() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [page, setPage] = useState(1);
  const query = useGetPage<Notice>("/notifications", {
    unread: unreadOnly ? "true" : undefined,
    page,
    per_page: 25,
  });
  const markRead = useWrite<{ id: string }>("POST", (b) => `/notifications/${b.id}/read`, {
    invalidate: ["/notifications"],
    toBody: () => undefined,
  });
  const readAll = useWrite("POST", "/notifications/read-all", {
    invalidate: ["/notifications"],
    success: t.notifications.allNotificationsMarkedAsRead,
  });

  const items = query.data?.data ?? [];
  const unreadCount = items.filter((n) => !(n.is_read ?? Boolean(n.read_at))).length;

  function openNotice(n: Notice) {
    const isRead = n.is_read ?? Boolean(n.read_at);
    if (!isRead) markRead.mutate({ id: n.id });
    if (n.link) navigate(n.link);
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.notifications}
        description={t.notifications.alertsForLowStockDebts}
        actions={
          unreadCount > 0 ? (
            <Button
              variant="outline"
              size="sm"
              disabled={readAll.isPending}
              onClick={() => readAll.mutate(undefined)}
            >
              <CheckCheck size={14} /> {t.notifications.markAllAsRead}
            </Button>
          ) : undefined
        }
      />

      <FilterBar>
        <SegmentedChips
          ariaLabel={t.notifications.filterNotifications}
          value={unreadOnly ? "unread" : "all"}
          onChange={(v) => {
            setUnreadOnly(v === "unread");
            setPage(1);
          }}
          options={[
            { value: "all", label: t.common.all },
            { value: "unread", label: t.notifications.unreadOnly },
          ]}
        />
      </FilterBar>

      {query.isLoading ? (
        <PageLoader />
      ) : items.length === 0 ? (
        <EmptyState
          title={
            unreadOnly ? t.notifications.noUnreadNotifications : t.notifications.youAreAllCaughtUp
          }
          description={
            unreadOnly
              ? t.notifications.switchToAllToReview
              : t.notifications.newAlertsForStockDebts
          }
        />
      ) : (
        <ul className="od-card-flush divide-y divide-[var(--od-border)] overflow-hidden">
          {items.map((n) => {
            const read = n.is_read ?? Boolean(n.read_at);
            return (
              <li
                key={n.id}
                className={cn(
                  "flex flex-wrap items-start justify-between gap-3 p-4 transition hover:bg-[var(--od-surface-subtle)]",
                  !read && "bg-[var(--od-surface-subtle)]/60",
                )}
              >
                <button
                  type="button"
                  onClick={() => openNotice(n)}
                  className="min-w-0 flex-1 text-left"
                >
                  <div className="flex items-center gap-2">
                    {!read && (
                      <span
                        className="h-2 w-2 shrink-0 rounded-full bg-[var(--od-info-mark)]"
                        aria-label={t.notifications.unread}
                      />
                    )}
                    <p
                      className={cn(
                        "text-sm text-[var(--od-ink)]",
                        !read ? "font-semibold" : "font-medium",
                      )}
                    >
                      {n.title ?? n.message}
                    </p>
                  </div>
                  {n.body && (
                    <p className="mt-0.5 text-sm text-[var(--od-ink-secondary)]">{n.body}</p>
                  )}
                  <p className="od-caption mt-1.5 inline-flex items-center gap-1">
                    <span>{formatDateTime(n.created_at)}</span>
                    {n.link && (
                      <>
                        <span>·</span>
                        <span className="inline-flex items-center gap-0.5 text-[var(--od-info)]">
                          {t.common.open} <ArrowUpRight size={12} />
                        </span>
                      </>
                    )}
                  </p>
                </button>
                {!read && (
                  <Button size="sm" variant="ghost" onClick={() => markRead.mutate({ id: n.id })}>
                    <Check size={14} /> {t.notifications.markRead}
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <Pager meta={query.data?.meta} onPage={setPage} />
    </div>
  );
}
