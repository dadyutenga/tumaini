import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { AuthLayout } from "@/components/layout/AuthLayout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/shared/kit";
import { ApiError, api } from "@/lib/api";

/** Two steps: ask for a code sent by SMS or email, then choose a new password with it. */
export default function ForgotPassword() {
  const navigate = useNavigate();
  const [step, setStep] = useState<"request" | "reset">("request");
  const [identifier, setIdentifier] = useState("");
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError("Something went wrong", 0));
    } finally {
      setBusy(false);
    }
  }

  const request = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      await api.post("/auth/forgot-password", { identifier }, { noRefresh: true });
      toast.success("If the account exists, a code is on its way.");
      setStep("reset");
    });
  };
  const reset = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      await api.post(
        "/auth/reset-password",
        { identifier, code, new_password: password },
        { noRefresh: true },
      );
      toast.success("Password changed. Sign in with the new one.");
      navigate("/login", { replace: true });
    });
  };

  return (
    <AuthLayout>
      <h1 className="mt-6 text-2xl font-semibold">Reset your password</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        {step === "request"
          ? "Enter your phone or email. We will send a 6 digit code."
          : "Enter the code we sent and choose a new password."}
      </p>
      <form onSubmit={step === "request" ? request : reset} className="mt-6 space-y-4" noValidate>
        <Field label="Phone or email" error={error?.fields.identifier}>
          <Input
            required
            autoFocus
            disabled={step === "reset"}
            value={identifier}
            onChange={(e) => setIdentifier(e.target.value)}
            autoComplete="username"
          />
        </Field>
        {step === "reset" && (
          <>
            <Field label="6 digit code" error={error?.fields.code}>
              <Input
                required
                inputMode="numeric"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                autoComplete="one-time-code"
              />
            </Field>
            <Field label="New password" error={error?.fields.new_password}>
              <Input
                required
                type="password"
                minLength={8}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
              />
            </Field>
          </>
        )}
        {error && (
          <p role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
            {error.message}
          </p>
        )}
        <Button type="submit" className="w-full" disabled={busy}>
          {busy ? "Please wait…" : step === "request" ? "Send code" : "Change password"}
        </Button>
      </form>
      <p className="mt-6 text-center text-sm text-muted-foreground">
        <Link className="text-primary underline" to="/login">
          Back to sign in
        </Link>
      </p>
    </AuthLayout>
  );
}
