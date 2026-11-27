import { useState, type FormEvent } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router-dom";
import { AlertCircle, Eye, EyeOff, ShieldCheck } from "lucide-react";
import { AuthLayout } from "@/components/layout/AuthLayout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { ApiError } from "@/lib/api";
import { useI18n } from "@/features/i18n/i18n";

export default function AuthPage({ mode }: { mode: string }) {
  const { t } = useI18n();
  const isRegister = mode === "register";
  const { status, login, verifyMfa, register } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? "/";
  const [busy, setBusy] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [mfaToken, setMfaToken] = useState<string | null>(null);
  const [mfaCode, setMfaCode] = useState("");
  const [remember, setRemember] = useState(() => {
    try {
      return localStorage.getItem("bms-remember") === "1";
    } catch {
      return false;
    }
  });
  const [error, setError] = useState<ApiError | null>(null);
  const [form, setForm] = useState(() => {
    let savedId = "";
    try {
      if (localStorage.getItem("bms-remember") === "1") {
        savedId = localStorage.getItem("bms-saved-identifier") ?? "";
      }
    } catch {
      /* ignore */
    }
    return {
      identifier: savedId,
      password: "",
      business_name: "",
      owner_name: "",
      phone: "",
      email: "",
    };
  });
  const set = (key: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [key]: e.target.value }));

  if (status === "authed") return <Navigate to={from} replace />;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (mfaToken) {
        await verifyMfa(mfaToken, mfaCode);
        navigate(from, { replace: true });
        return;
      }
      if (isRegister) {
        await register({
          business_name: form.business_name,
          owner_name: form.owner_name,
          phone: form.phone,
          email: form.email,
          password: form.password,
        });
      } else {
        const outcome = await login({ identifier: form.identifier, password: form.password });
        try {
          if (remember) {
            localStorage.setItem("bms-remember", "1");
            localStorage.setItem("bms-saved-identifier", form.identifier);
          } else {
            localStorage.removeItem("bms-remember");
            localStorage.removeItem("bms-saved-identifier");
          }
        } catch {
          /* ignore */
        }
        if (outcome.mfaRequired && outcome.mfaToken) {
          setMfaToken(outcome.mfaToken);
          setMfaCode("");
          return;
        }
      }
      navigate(from, { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(t.common.somethingWentWrong, 0));
    } finally {
      setBusy(false);
    }
  }

  if (mfaToken) {
    return (
      <AuthLayout>
        <div className="flex items-start gap-3">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] text-[var(--od-ink)]">
            <ShieldCheck size={18} />
          </div>
          <div>
            <h1 className="text-[22px] font-semibold tracking-tight text-[var(--od-ink)]">
              {t.auth.twoFactorVerification}
            </h1>
            <p className="mt-1 text-sm text-[var(--od-ink-secondary)]">
              {t.auth.enterThe6DigitCode}
            </p>
          </div>
        </div>

        <form onSubmit={submit} className="mt-6 space-y-4" noValidate>
          <Field label={t.auth.verificationCode}>
            <Input
              required
              autoFocus
              autoComplete="one-time-code"
              className="od-mono tracking-widest"
              placeholder={t.common.n123456OrXxxxXxxx}
              value={mfaCode}
              onChange={(e) => setMfaCode(e.target.value)}
            />
          </Field>

          {error && (
            <div
              role="alert"
              className="flex items-start gap-2 rounded-xl border border-[var(--od-bad-mark)]/30 bg-[var(--od-bad-soft,rgba(239,68,68,0.08))] p-3 text-sm text-[var(--od-bad)]"
            >
              <AlertCircle size={16} className="mt-0.5 shrink-0" />
              <span>{error.message}</span>
            </div>
          )}

          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setMfaToken(null);
                setMfaCode("");
                setError(null);
              }}
            >
              {t.common.back}
            </Button>
            <Button type="submit" className="flex-1" disabled={busy}>
              {busy ? t.auth.verifying : t.auth.verifyAndSignIn}
            </Button>
          </div>
        </form>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <div>
        <h1 className="text-[22px] font-semibold tracking-tight text-[var(--od-ink)]">
          {isRegister ? t.auth.createYourBusiness : t.common.signIn}
        </h1>
        <p className="mt-1 text-sm text-[var(--od-ink-secondary)]">
          {isRegister ? t.auth.setUpTheBusinessIts : t.auth.useYourPhoneNumberOr}
        </p>
      </div>

      <form onSubmit={submit} className="mt-6 space-y-4" noValidate>
        {isRegister ? (
          <>
            <Field label={t.common.businessName} error={error?.fields.business_name}>
              <Input
                required
                value={form.business_name}
                onChange={set("business_name")}
                autoComplete="organization"
              />
            </Field>
            <Field label={t.auth.yourName} error={error?.fields.owner_name}>
              <Input
                required
                value={form.owner_name}
                onChange={set("owner_name")}
                autoComplete="name"
              />
            </Field>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field label={t.common.phone} error={error?.fields.phone}>
                <Input value={form.phone} onChange={set("phone")} autoComplete="tel" />
              </Field>
              <Field label={t.common.email} error={error?.fields.email}>
                <Input
                  type="email"
                  value={form.email}
                  onChange={set("email")}
                  autoComplete="email"
                />
              </Field>
            </div>
          </>
        ) : (
          <Field label={t.auth.phoneOrEmail} error={error?.fields.identifier}>
            <Input
              required
              autoFocus
              value={form.identifier}
              onChange={set("identifier")}
              autoComplete="username"
            />
          </Field>
        )}

        <Field label={t.auth.password} error={error?.fields.password}>
          <div className="relative">
            <Input
              required
              type={showPassword ? "text" : "password"}
              minLength={isRegister ? 8 : undefined}
              value={form.password}
              onChange={set("password")}
              autoComplete={isRegister ? "new-password" : "current-password"}
              className="pr-10"
            />
            <button
              type="button"
              aria-label={showPassword ? t.common.hidePassword : t.common.showPassword}
              onClick={() => setShowPassword((s) => !s)}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 rounded-md p-1 text-[var(--od-ink-muted)] transition hover:text-[var(--od-ink)]"
            >
              {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
            </button>
          </div>
        </Field>

        {!isRegister && (
          <div className="flex items-center justify-between gap-2 text-sm">
            <label className="flex cursor-pointer items-center gap-2 text-[var(--od-ink-secondary)]">
              <input
                type="checkbox"
                checked={remember}
                onChange={(e) => setRemember(e.target.checked)}
              />
              {t.auth.rememberMe}
            </label>
            <Link
              className="font-medium text-[var(--od-link)] underline-offset-4 hover:underline"
              to="/forgot-password"
            >
              {t.auth.forgotPassword2}
            </Link>
          </div>
        )}

        {error && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-xl border border-[var(--od-bad-mark)]/30 bg-[var(--od-bad-soft,rgba(239,68,68,0.08))] p-3 text-sm text-[var(--od-bad)]"
          >
            <AlertCircle size={16} className="mt-0.5 shrink-0" />
            <span>{error.message}</span>
          </div>
        )}

        <Button type="submit" className="w-full" disabled={busy}>
          {busy ? t.auth.pleaseWait : isRegister ? t.auth.createBusiness : t.common.signIn}
        </Button>
      </form>

      <p className="mt-6 border-t border-[var(--od-border)] pt-5 text-center text-sm text-[var(--od-ink-secondary)]">
        {isRegister ? (
          <>
            {t.auth.alreadyHaveAnAccount}{" "}
            <Link
              className="font-medium text-[var(--od-link)] underline-offset-4 hover:underline"
              to="/login"
            >
              {t.common.signIn}
            </Link>
          </>
        ) : (
          <>
            {t.auth.newBusiness}{" "}
            <Link
              className="font-medium text-[var(--od-link)] underline-offset-4 hover:underline"
              to="/register"
            >
              {t.common.register}
            </Link>
          </>
        )}
      </p>
    </AuthLayout>
  );
}
