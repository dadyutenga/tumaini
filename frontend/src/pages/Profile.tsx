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
  const { t, locale, setLocale } = useI18n();
  const [name, setName] = useState(user?.name ?? "");
  const [lang, setLang] = useState<Locale>((user?.language as Locale) || locale);
  const [pw, setPw] = useState({ current_password: "", new_password: "" });

  const sessions = useGet<SessionItem[]>("/me/sessions");
  const logins = useGet<LoginEvent[]>("/me/login-history", { per_page: 10 });

  const update = useWrite<{ name: string; language: Locale }, unknown>("PUT", "/me", {
    success: t.settings.profileUpdated,
    onSuccess: () => {
      setLocale(lang);
      void reloadMe();
    },
  });
  const change = useWrite<typeof pw, unknown>("PUT", "/auth/password", {
    success: t.settings.passwordChanged,
    onSuccess: () => {
      setPw({ current_password: "", new_password: "" });
      void reloadMe();
    },
  });
  const endSession = useWrite<{ id: string }>("DELETE", (b) => `/me/sessions/${b.id}`, {
    invalidate: ["/me/sessions"],
    success: t.settings.sessionSignedOut,
  });

  return (
    <div className="space-y-8">
      <PageHeader
        title={t.settings.myProfile}
        description={`${user?.role_name ?? ""}${me?.is_owner ? ` ${t.settings.owner}` : ""}`}
        badge={me?.is_owner ? <Badge variant="secondary">{t.common.owner}</Badge> : undefined}
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
            <h2 className="od-card-title text-[var(--od-ink)]">{t.settings.profileDetails}</h2>
            <p className="od-card-subtitle">{t.settings.updateYourDisplayNameAnd}</p>
          </div>
          <Field label={t.common.name}>
            <Input required value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t.common.role}>
            <Input disabled value={`${user?.role_name ?? ""}${me?.is_owner ? " (Owner)" : ""}`} />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t.common.phone}>
              <Input disabled className="od-mono" value={user?.phone ?? "-"} />
            </Field>
            <Field label={t.common.email}>
              <Input disabled value={user?.email ?? "-"} />
            </Field>
          </div>
          <Field label={t.settings.language2}>
            <NativeSelect value={lang} onChange={(e) => setLang(e.target.value as Locale)}>
              <option value="en">{t.settings.english}</option>
              <option value="sw">{t.settings.kiswahiliSwahili}</option>
            </NativeSelect>
          </Field>
          <div className="border-t border-[var(--od-border)] pt-3">
            <Button type="submit" disabled={update.isPending}>
              {t.settings.saveChanges}
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
            <h2 className="od-card-title text-[var(--od-ink)]">{t.common.changePassword}</h2>
            <p className="od-card-subtitle">{t.settings.useAtLeast8Characters}</p>
          </div>
          <Field label={t.common.currentPassword}>
            <Input
              required
              type="password"
              autoComplete="current-password"
              value={pw.current_password}
              onChange={(e) => setPw({ ...pw, current_password: e.target.value })}
            />
          </Field>
          <Field label={t.common.newPassword}>
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
              {t.common.changePassword}
            </Button>
          </div>
        </form>
      </div>

      <section className="max-w-4xl space-y-3">
        <div>
          <h2 className="od-section-title">{t.settings.activeSessions}</h2>
          <p className="od-caption mt-0.5">{t.settings.devicesCurrentlySignedIntoYour}</p>
        </div>
        <DataGrid
          rows={sessions.data}
          loading={sessions.isLoading}
          rowKey={(s) => s.id}
          empty={t.settings.noActiveSessions}
          columns={[
            {
              header: t.common.device,
              cell: (s) => (
                <span
                  className="line-clamp-1 max-w-xs text-xs text-[var(--od-ink)]"
                  title={s.user_agent ?? ""}
                >
                  {s.user_agent || t.settings.browserSession}
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
              header: t.settings.lastActive,
              cell: (s) => <span className="od-tabular">{formatDateTime(s.last_used_at)}</span>,
            },
            {
              header: "",
              className: "text-right",
              cell: (s) => (
                <div className="flex justify-end">
                  {s.current ? (
                    <Badge variant="secondary">{t.settings.current}</Badge>
                  ) : (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                      onClick={() => endSession.mutate({ id: s.id })}
                    >
                      {t.common.signOut}
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
          <h2 className="od-section-title">{t.settings.recentLogins}</h2>
          <p className="od-caption mt-0.5">{t.settings.latest10SignInAttempts}</p>
        </div>
        <DataGrid
          rows={logins.data}
          loading={logins.isLoading}
          rowKey={(l) => l.id}
          empty={t.settings.noRecentLoginsRecorded}
          columns={[
            {
              header: t.common.time,
              cell: (l) => <span className="od-tabular">{formatDateTime(l.created_at)}</span>,
            },
            {
              header: t.common.result,
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
              header: t.common.device,
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
