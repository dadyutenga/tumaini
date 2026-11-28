import { useEffect, useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { AuthLayout } from "@/components/layout/AuthLayout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/shared/kit";
import { api, ApiError } from "@/lib/api";

export default function SetupPage() {
  const navigate = useNavigate();
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
      setError("Passwords do not match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.post("/setup", form, { noRefresh: true });
      navigate("/settings", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Setup could not be completed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthLayout>
      <div>
        <h1 className="text-[22px] font-semibold">Set up your business</h1>
        <p className="mt-1 text-sm text-[var(--od-ink-secondary)]">
          Create the business profile and the first administrator account.
        </p>
      </div>
      <form onSubmit={submit} className="mt-6 space-y-4" noValidate>
        <Field label="Business name">
          <Input required autoComplete="organization" value={form.business_name} onChange={set("business_name")} />
        </Field>
        <Field label="Your name">
          <Input required autoComplete="name" value={form.owner_name} onChange={set("owner_name")} />
        </Field>
        <Field label="Phone">
          <Input required autoComplete="tel" value={form.phone} onChange={set("phone")} />
        </Field>
        <Field label="Email">
          <Input required type="email" autoComplete="email" value={form.email} onChange={set("email")} />
        </Field>
        <Field label="Password">
          <Input required type="password" minLength={8} autoComplete="new-password" value={form.password} onChange={set("password")} />
        </Field>
        <Field label="Confirm password">
          <Input required type="password" minLength={8} autoComplete="new-password" value={form.confirm_password} onChange={set("confirm_password")} />
        </Field>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        <Button type="submit" className="w-full" disabled={busy}>
          {busy ? "Setting up…" : "Create business"}
        </Button>
      </form>
    </AuthLayout>
  );
}
