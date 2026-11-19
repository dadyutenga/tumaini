import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Bell, Check, CheckCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader, Pager } from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { useGetPage, useWrite } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";
import { cn } from "@/lib/utils";

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
    success: "All notifications marked as read",
  });

  const items = query.data?.data ?? [];
  const unreadCount = items.filter((n) => !(n.is_read ?? Boolean(n.read_at))).length;

  function openNotice(n: Notice) {
    const isRead = n.is_read ?? Boolean(n.read_at);
    if (!isRead) markRead.mutate({ id: n.id });
    if (n.link) navigate(n.link);
  }

  return (
    <>
      <PageHeader
        title="Notifications"
        description="Alerts for low stock, debts, overdue tasks, renewals, approvals and security."
        actions={
          <div className="flex gap-2">
            <Button
              variant={unreadOnly ? "default" : "outline"}
              size="sm"
              onClick={() => {
                setUnreadOnly((u) => !u);
                setPage(1);
              }}
            >
              Unread only
            </Button>
            {unreadCount > 0 && (
              <Button
                variant="outline"
                size="sm"
                disabled={readAll.isPending}
                onClick={() => readAll.mutate(undefined)}
              >
                <CheckCheck size={14} /> Mark all as read
              </Button>
            )}
          </div>
        }
      />

      {query.isLoading ? (
        <PageLoader />
      ) : items.length === 0 ? (
        <div className="rounded-2xl border bg-card p-10 text-center text-sm text-muted-foreground">
          <Bell className="mx-auto mb-2 opacity-40" size={28} />
          {unreadOnly ? "No unread notifications." : "You are all caught up."}
        </div>
      ) : (
        <ul className="divide-y overflow-hidden rounded-2xl border bg-card">
          {items.map((n) => {
            const read = n.is_read ?? Boolean(n.read_at);
            return (
              <li
                key={n.id}
                className={cn(
                  "flex flex-wrap items-start justify-between gap-3 p-4 transition hover:bg-muted/40",
                  !read && "bg-primary/5",
                )}
              >
                <button
                  type="button"
                  onClick={() => openNotice(n)}
                  className="min-w-0 flex-1 text-left"
                >
                  <div className="flex items-center gap-2">
                    {!read && (
                      <span className="h-2 w-2 shrink-0 rounded-full bg-primary" aria-label="Unread" />
                    )}
                    <p className={cn("text-sm", !read ? "font-semibold" : "font-medium")}>
                      {n.title ?? n.message}
                    </p>
                  </div>
                  {n.body && <p className="mt-0.5 text-sm text-muted-foreground">{n.body}</p>}
                  <p className="mt-1 text-xs text-muted-foreground">
                    {formatDateTime(n.created_at)}
                    {n.link ? " · Click to open" : ""}
                  </p>
                </button>
                {!read && (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => markRead.mutate({ id: n.id })}
                  >
                    <Check size={14} /> Mark read
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <Pager meta={query.data?.meta} onPage={setPage} />
    </>
  );
}
