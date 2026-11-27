import { useState } from "react";
import { KeyRound, ShieldCheck, ShieldOff } from "lucide-react";
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
import { CopyButton } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { useI18n } from "@/features/i18n/i18n";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";
import type { Locale } from "@/types";

interface MFAStatus {
  enabled: boolean;
  factor_type?: string;
  confirmed_at?: string | null;
  recovery_codes_remaining: number;
  recommended: boolean;
}

interface MFASetupPayload {
  secret: string;
  otpauth_url: string;
  issuer: string;
}

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
  const mfa = useGet<MFAStatus>("/me/mfa");

  const [mfaSetupOpen, setMfaSetupOpen] = useState(false);
  const [mfaSetupData, setMfaSetupData] = useState<MFASetupPayload | null>(null);
  const [mfaPassword, setMfaPassword] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [mfaAction, setMfaAction] = useState<"disable" | "regen" | null>(null);

  const startMfa = useWrite<{ current_password: string }, MFASetupPayload>(
    "POST",
    "/me/mfa/setup",
    {
      onSuccess: (res) => {
        setMfaSetupData(res);
        setMfaPassword("");
        setMfaCode("");
      },
    },
  );
  const confirmMfa = useWrite<{ code: string }, { enabled: boolean; recovery_codes: string[] }>(
    "POST",
    "/me/mfa/confirm",
    {
      invalidate: ["/me/mfa"],
      success: t.settings.twoFactorAuthenticationEnabled,
      onSuccess: (res) => {
        setRecoveryCodes(res.recovery_codes);
        setMfaSetupOpen(false);
        setMfaSetupData(null);
        setMfaCode("");
        void reloadMe();
      },
    },
  );
  const disableMfa = useWrite<{ current_password: string; code: string }>(
    "POST",
    "/me/mfa/disable",
    {
      invalidate: ["/me/mfa"],
      success: t.settings.twoFactorAuthenticationDisabled,
      onSuccess: () => {
        setMfaAction(null);
        setMfaPassword("");
        setMfaCode("");
        void reloadMe();
      },
    },
  );
  const regenCodes = useWrite<
    { current_password: string; code: string },
    { recovery_codes: string[] }
  >("POST", "/me/mfa/recovery-codes", {
    invalidate: ["/me/mfa"],
    success: t.settings.newRecoveryCodesGenerated,
    onSuccess: (res) => {
      setRecoveryCodes(res.recovery_codes);
      setMfaAction(null);
      setMfaPassword("");
      setMfaCode("");
    },
  });

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

      <section className="od-card max-w-4xl space-y-4 p-6">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <h2 className="od-card-title text-[var(--od-ink)]">
                {t.settings.twoFactorAuthentication}
              </h2>
              {mfa.data?.enabled ? (
                <StatusBadge value="active" />
              ) : (
                <Badge variant="outline">{t.settings.notEnabled}</Badge>
              )}
              {mfa.data?.recommended && !mfa.data?.enabled && (
                <Badge variant="secondary">{t.settings.recommendedForYourRole}</Badge>
              )}
            </div>
            <p className="od-card-subtitle">{t.settings.protectYourAccountWithA}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            {mfa.data?.enabled ? (
              <>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setMfaPassword("");
                    setMfaCode("");
                    setMfaAction("regen");
                  }}
                >
                  <KeyRound size={14} /> {t.settings.regenerateCodes}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                  onClick={() => {
                    setMfaPassword("");
                    setMfaCode("");
                    setMfaAction("disable");
                  }}
                >
                  <ShieldOff size={14} /> {t.settings.disableMfa}
                </Button>
              </>
            ) : (
              <Button
                size="sm"
                onClick={() => {
                  setMfaSetupData(null);
                  setMfaPassword("");
                  setMfaCode("");
                  setMfaSetupOpen(true);
                }}
              >
                <ShieldCheck size={14} /> {t.settings.enableTwoFactor}
              </Button>
            )}
          </div>
        </div>

        {mfa.data?.enabled && (
          <div className="flex flex-wrap items-center gap-6 border-t border-[var(--od-border)] pt-3 text-xs text-[var(--od-ink-secondary)]">
            <span>
              {t.settings.method}{" "}
              <strong className="text-[var(--od-ink)]">{t.settings.authenticatorAppTotp}</strong>
            </span>
            <span>
              {t.settings.recoveryCodesRemaining}{" "}
              <strong className="od-tabular text-[var(--od-ink)]">
                {mfa.data.recovery_codes_remaining}
              </strong>
            </span>
            {mfa.data.confirmed_at && (
              <span>
                {t.settings.enabled}{" "}
                <span className="od-tabular">{formatDateTime(mfa.data.confirmed_at)}</span>
              </span>
            )}
          </div>
        )}

        {recoveryCodes && (
          <div className="space-y-3 rounded-xl border border-[var(--od-info-mark)]/30 bg-[var(--od-surface-subtle)] p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p className="text-sm font-semibold text-[var(--od-ink)]">
                  {t.settings.saveYourSingleUseRecovery}
                </p>
                <p className="od-caption">{t.settings.storeTheseSafelyEachCode}</p>
              </div>
              <div className="flex items-center gap-2">
                <CopyButton text={recoveryCodes.join("\n")} />
                <Button size="sm" variant="ghost" onClick={() => setRecoveryCodes(null)}>
                  {t.common.done}
                </Button>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
              {recoveryCodes.map((c) => (
                <code
                  key={c}
                  className="od-mono rounded-lg border border-[var(--od-border)] bg-[var(--od-surface)] px-2.5 py-1.5 text-center text-xs font-medium text-[var(--od-ink)]"
                >
                  {c}
                </code>
              ))}
            </div>
          </div>
        )}
      </section>

      <FormDialog
        open={mfaSetupOpen}
        onOpenChange={(o) => {
          if (!o) {
            setMfaSetupOpen(false);
            setMfaSetupData(null);
            setMfaPassword("");
            setMfaCode("");
          }
        }}
        title={
          mfaSetupData
            ? t.settings.verifyYourAuthenticatorApp
            : t.settings.enableTwoFactorAuthentication
        }
        description={
          mfaSetupData ? t.settings.addThisSecretKeyIn : t.settings.confirmYourCurrentPasswordTo
        }
        submitLabel={mfaSetupData ? t.settings.verifyAndEnable : t.settings.continue}
        busy={startMfa.isPending || confirmMfa.isPending}
        onSubmit={() => {
          if (!mfaSetupData) {
            startMfa.mutate({ current_password: mfaPassword });
          } else {
            confirmMfa.mutate({ code: mfaCode });
          }
        }}
      >
        {!mfaSetupData ? (
          <Field label={t.common.currentPassword}>
            <Input
              required
              type="password"
              autoComplete="current-password"
              value={mfaPassword}
              onChange={(e) => setMfaPassword(e.target.value)}
            />
          </Field>
        ) : (
          <>
            <div className="space-y-2 rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-3">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-medium text-[var(--od-ink-secondary)]">
                  {t.settings.setupSecretKey}
                </span>
                <CopyButton text={mfaSetupData.secret} />
              </div>
              <code className="od-mono block break-all text-xs font-semibold text-[var(--od-ink)]">
                {mfaSetupData.secret}
              </code>
              <div className="flex items-center justify-between gap-2 border-t border-[var(--od-border)] pt-2">
                <span className="text-xs text-[var(--od-ink-muted)]">
                  {t.settings.authenticatorUri}
                </span>
                <CopyButton text={mfaSetupData.otpauth_url} />
              </div>
            </div>
            <Field label="6-digit verification code *">
              <Input
                required
                inputMode="numeric"
                autoComplete="one-time-code"
                className="od-mono tracking-widest"
                placeholder="123456"
                value={mfaCode}
                onChange={(e) => setMfaCode(e.target.value)}
              />
            </Field>
          </>
        )}
      </FormDialog>

      <FormDialog
        open={mfaAction !== null}
        onOpenChange={(o) => {
          if (!o) {
            setMfaAction(null);
            setMfaPassword("");
            setMfaCode("");
          }
        }}
        title={
          mfaAction === "disable"
            ? t.settings.disableTwoFactorAuthentication
            : t.settings.regenerateRecoveryCodes
        }
        description={
          mfaAction === "disable"
            ? t.settings.confirmYourPasswordAndEnter
            : t.settings.thisReplacesAllUnusedRecovery
        }
        submitLabel={mfaAction === "disable" ? t.settings.disableMfa : t.settings.generateNewCodes}
        busy={disableMfa.isPending || regenCodes.isPending}
        onSubmit={() => {
          if (mfaAction === "disable") {
            disableMfa.mutate({ current_password: mfaPassword, code: mfaCode });
          } else if (mfaAction === "regen") {
            regenCodes.mutate({ current_password: mfaPassword, code: mfaCode });
          }
        }}
      >
        <Field label={t.common.currentPassword}>
          <Input
            required
            type="password"
            autoComplete="current-password"
            value={mfaPassword}
            onChange={(e) => setMfaPassword(e.target.value)}
          />
        </Field>
        <Field
          label={
            mfaAction === "disable"
              ? t.common.authenticatorOrRecoveryCode
              : t.settings.n6DigitTotpCode
          }
        >
          <Input
            required
            autoComplete="one-time-code"
            className="od-mono tracking-widest"
            placeholder={mfaAction === "disable" ? t.common.n123456OrXxxxXxxx : "123456"}
            value={mfaCode}
            onChange={(e) => setMfaCode(e.target.value)}
          />
        </Field>
      </FormDialog>

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
