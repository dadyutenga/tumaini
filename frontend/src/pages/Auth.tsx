import { useState, type FormEvent } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router-dom";
import { AuthLayout } from "@/components/layout/AuthLayout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { ApiError } from "@/lib/api";

export default function AuthPage({ mode }: { mode: string }) {
  const isRegister = mode === "register";
  const { status, login, register } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? "/";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [form, setForm] = useState({
    identifier: "",
    password: "",
    business_name: "",
    owner_name: "",
    phone: "",
    email: "",
  });
  const set = (key: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [key]: e.target.value }));

  if (status === "authed") return <Navigate to={from} replace />;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (isRegister) {
        await register({
          business_name: form.business_name,
          owner_name: form.owner_name,
          phone: form.phone,
          email: form.email,
          password: form.password,
        });
      } else {
        await login({ identifier: form.identifier, password: form.password });
      }
      navigate(from, { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError("Something went wrong", 0));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthLayout>
      <h1 className="mt-6 text-2xl font-semibold">
        {isRegister ? "Create your business" : "Sign in"}
      </h1>
      <p className="mt-1 text-sm text-muted-foreground">
        {isRegister
          ? "Set up the business, its main branch and the owner account."
          : "Use your phone number or email and your password."}
      </p>
      <form onSubmit={submit} className="mt-6 space-y-4" noValidate>
        {isRegister ? (
          <>
            <Field label="Business name" error={error?.fields.business_name}>
              <Input
                required
                value={form.business_name}
                onChange={set("business_name")}
                autoComplete="organization"
              />
            </Field>
            <Field label="Your name" error={error?.fields.owner_name}>
              <Input
                required
                value={form.owner_name}
                onChange={set("owner_name")}
                autoComplete="name"
              />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Phone" error={error?.fields.phone}>
                <Input value={form.phone} onChange={set("phone")} autoComplete="tel" />
              </Field>
              <Field label="Email" error={error?.fields.email}>
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
          <Field label="Phone or email" error={error?.fields.identifier}>
            <Input
              required
              autoFocus
              value={form.identifier}
              onChange={set("identifier")}
              autoComplete="username"
            />
          </Field>
        )}
        <Field label="Password" error={error?.fields.password}>
          <Input
            required
            type="password"
            minLength={isRegister ? 8 : undefined}
            value={form.password}
            onChange={set("password")}
            autoComplete={isRegister ? "new-password" : "current-password"}
          />
        </Field>
        {!isRegister && (
          <p className="text-right text-sm">
            <Link className="text-primary underline" to="/forgot-password">
              Forgot password?
            </Link>
          </p>
        )}
        {error && (
          <p role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
            {error.message}
          </p>
        )}
        <Button type="submit" className="w-full" disabled={busy}>
          {busy ? "Please wait…" : isRegister ? "Create business" : "Sign in"}
        </Button>
      </form>
      <p className="mt-6 text-center text-sm text-muted-foreground">
        {isRegister ? (
          <>
            Already have an account?{" "}
            <Link className="text-primary underline" to="/login">
              Sign in
            </Link>
          </>
        ) : (
          <>
            New business?{" "}
            <Link className="text-primary underline" to="/register">
              Register
            </Link>
          </>
        )}
      </p>
    </AuthLayout>
  );
}
