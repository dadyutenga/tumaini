import { useState } from "react";
import { KeyRound } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useI18n } from "@/features/i18n/i18n";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";
import type { Locale } from "@/types";

interface SessionItem {
  id: string;
  ip: string | null;
  user_agent: string | null;
  last_used_at: string;
  created_at: string;
  current?: boolean;
}

interface LoginEvent {
  id: string;
  identifier: string;
  success: boolean;
  reason: string | null;
  ip: string | null;
  user_agent: string | null;
  created_at: string;
}

export default function Profile() {
  const { user, me, reloadMe } = useAuth();
  const { locale, setLocale } = useI18n();
  const [name, setName] = useState(user?.name ?? "");
  const [lang, setLang] = useState<Locale>((user?.language as Locale) || locale);
  const [pw, setPw] = useState({ current_password: "", new_password: "" });

  const sessions = useGet<SessionItem[]>("/me/sessions");
  const logins = useGet<LoginEvent[]>("/me/login-history", { per_page: 10 });

  const update = useWrite<{ name: string; language: Locale }, unknown>("PUT", "/me", {
    success: "Profile updated",
    onSuccess: () => {
      setLocale(lang);
      void reloadMe();
    },
  });
  const change = useWrite<typeof pw, unknown>("PUT", "/auth/password", {
    success: "Password changed",
    onSuccess: () => {
      setPw({ current_password: "", new_password: "" });
      void reloadMe();
    },
  });
  const endSession = useWrite<{ id: string }>("DELETE", (b) => `/me/sessions/${b.id}`, {
    invalidate: ["/me/sessions"],
    success: "Session signed out",
  });

  return (
    <div className="space-y-8">
      <PageHeader
        title="My profile"
        description={`${user?.role_name ?? ""}${me?.is_owner ? " · Owner" : ""}`}
        badge={me?.is_owner ? <Badge variant="secondary">Owner</Badge> : undefined}
      />

      <div className="grid max-w-4xl gap-5 md:grid-cols-2">
        <form
          className="od-card space-y-4 p-6"
          onSubmit={(e) => {
            e.preventDefault();
            update.mutate({ name, language: lang });
          }}
        >
          <div>
            <h2 className="od-card-title text-[var(--od-ink)]">Profile details</h2>
            <p className="od-card-subtitle">Update your display name and interface language.</p>
          </div>
          <Field label="Name *">
            <Input required value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Role">
            <Input disabled value={`${user?.role_name ?? ""}${me?.is_owner ? " (Owner)" : ""}`} />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Phone">
              <Input disabled className="od-mono" value={user?.phone ?? "-"} />
            </Field>
            <Field label="Email">
              <Input disabled value={user?.email ?? "-"} />
            </Field>
          </div>
          <Field label="Language">
            <NativeSelect value={lang} onChange={(e) => setLang(e.target.value as Locale)}>
              <option value="en">English</option>
              <option value="sw">Kiswahili (Swahili)</option>
            </NativeSelect>
          </Field>
          <div className="border-t border-[var(--od-border)] pt-3">
            <Button type="submit" disabled={update.isPending}>
              Save changes
            </Button>
          </div>
        </form>

        <form
          id="password"
          className="od-card space-y-4 p-6"
          onSubmit={(e) => {
            e.preventDefault();
            change.mutate(pw);
          }}
        >
          <div>
            <h2 className="od-card-title text-[var(--od-ink)]">Change password</h2>
            <p className="od-card-subtitle">Use at least 8 characters for a strong password.</p>
          </div>
          <Field label="Current password *">
            <Input
              required
              type="password"
              autoComplete="current-password"
              value={pw.current_password}
              onChange={(e) => setPw({ ...pw, current_password: e.target.value })}
            />
          </Field>
          <Field label="New password *">
            <Input
              required
              minLength={8}
              type="password"
              autoComplete="new-password"
              value={pw.new_password}
              onChange={(e) => setPw({ ...pw, new_password: e.target.value })}
            />
          </Field>
          <div className="border-t border-[var(--od-border)] pt-3">
            <Button type="submit" disabled={change.isPending}>
              Change password
            </Button>
          </div>
        </form>
      </div>

      <section className="max-w-4xl space-y-3">
        <div>
          <h2 className="od-section-title">Active sessions</h2>
          <p className="od-caption mt-0.5">Devices currently signed into your account.</p>
        </div>
        <DataGrid
          rows={sessions.data}
          loading={sessions.isLoading}
          rowKey={(s) => s.id}
          empty="No active sessions"
          columns={[
            {
              header: "Device",
              cell: (s) => (
                <span
                  className="line-clamp-1 max-w-xs text-xs text-[var(--od-ink)]"
                  title={s.user_agent ?? ""}
                >
                  {s.user_agent || "Browser session"}
                </span>
              ),
            },
            {
              header: "IP",
              cell: (s) => (
                <span className="od-mono text-xs text-[var(--od-ink-secondary)]">
                  {s.ip ?? "-"}
                </span>
              ),
            },
            {
              header: "Last active",
              cell: (s) => <span className="od-tabular">{formatDateTime(s.last_used_at)}</span>,
            },
            {
              header: "",
              className: "text-right",
              cell: (s) => (
                <div className="flex justify-end">
                  {s.current ? (
                    <Badge variant="secondary">Current</Badge>
                  ) : (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                      onClick={() => endSession.mutate({ id: s.id })}
                    >
                      Sign out
                    </Button>
                  )}
                </div>
              ),
            },
          ]}
        />
      </section>

      <section className="max-w-4xl space-y-3">
        <div>
          <h2 className="od-section-title">Recent logins</h2>
          <p className="od-caption mt-0.5">Latest 10 sign-in attempts on your account.</p>
        </div>
        <DataGrid
          rows={logins.data}
          loading={logins.isLoading}
          rowKey={(l) => l.id}
          empty="No recent logins recorded"
          columns={[
            {
              header: "Time",
              cell: (l) => <span className="od-tabular">{formatDateTime(l.created_at)}</span>,
            },
            {
              header: "Result",
              cell: (l) => <StatusBadge value={l.success ? "active" : "rejected"} />,
            },
            {
              header: "IP",
              cell: (l) => (
                <span className="od-mono text-xs text-[var(--od-ink-secondary)]">
                  {l.ip ?? "-"}
                </span>
              ),
            },
            {
              header: "Device",
              cell: (l) => (
                <span className="line-clamp-1 max-w-xs text-xs text-[var(--od-ink-muted)]">
                  {l.user_agent || "-"}
                </span>
              ),
            },
          ]}
        />
      </section>
    </div>
  );
}
