import { useEffect, useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { AuthLayout } from "@/components/layout/AuthLayout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/shared/kit";
import { api, ApiError } from "@/lib/api";
import { useAuth, type SessionResult } from "@/features/auth/auth";
import { useI18n } from "@/features/i18n/i18n";

export default function SetupPage() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const { completeSetup } = useAuth();
  const [status, setStatus] = useState<"loading" | "needed" | "done">("loading");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({
    business_name: "",
    owner_name: "",
    phone: "",
    email: "",
    password: "",
    confirm_password: "",
  });

  useEffect(() => {
    void api
      .get<{ needs_setup: boolean }>("/setup/status")
      .then((v) => setStatus(v.needs_setup ? "needed" : "done"))
      .catch(() => setStatus("done"));
  }, []);

  if (status === "done") return <Navigate to="/login" replace />;
  if (status === "loading") return null;

  const set = (key: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((old) => ({ ...old, [key]: e.target.value }));

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (form.password !== form.confirm_password) {
      setError(t.auth.passwordsDoNotMatch);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const { business_name, owner_name, phone, email, password } = form;
      const result = await api.post<SessionResult>(
        "/setup",
        { business_name, owner_name, phone, email, password },
        { noRefresh: true },
      );
      await completeSetup(result);
      navigate("/settings", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t.auth.setupCouldNotBeCompleted);
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthLayout>
      <div>
        <h1 className="text-[22px] font-semibold">{t.auth.setUpYourBusiness}</h1>
        <p className="mt-1 text-sm text-[var(--od-ink-secondary)]">
          {t.auth.createTheBusinessProfile}
        </p>
      </div>
      <form onSubmit={submit} className="mt-6 space-y-4" noValidate>
        <Field label={t.common.businessName}>
          <Input
            required
            autoComplete="organization"
            value={form.business_name}
            onChange={set("business_name")}
          />
        </Field>
        <Field label={t.auth.yourName}>
          <Input
            required
            autoComplete="name"
            value={form.owner_name}
            onChange={set("owner_name")}
          />
        </Field>
        <Field label={t.common.phone}>
          <Input required autoComplete="tel" value={form.phone} onChange={set("phone")} />
        </Field>
        <Field label={t.common.email}>
          <Input
            required
            type="email"
            autoComplete="email"
            value={form.email}
            onChange={set("email")}
          />
        </Field>
        <Field label={t.auth.password}>
          <Input
            required
            type="password"
            minLength={8}
            autoComplete="new-password"
            value={form.password}
            onChange={set("password")}
          />
        </Field>
        <Field label={t.auth.confirmPassword}>
          <Input
            required
            type="password"
            minLength={8}
            autoComplete="new-password"
            value={form.confirm_password}
            onChange={set("confirm_password")}
          />
        </Field>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <Button type="submit" className="w-full" disabled={busy}>
          {busy ? t.auth.settingUp : t.auth.createBusiness}
        </Button>
      </form>
    </AuthLayout>
  );
}
