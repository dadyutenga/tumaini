import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { AlertCircle, ArrowLeft } from "lucide-react";
import { toast } from "sonner";
import { AuthLayout } from "@/components/layout/AuthLayout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/shared/kit";
import { ApiError, api } from "@/lib/api";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

/** Two steps: ask for a code sent by SMS or email, then choose a new password with it. */
export default function ForgotPassword() {
  const { t } = useI18n();
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
      setError(err instanceof ApiError ? err : new ApiError(t.common.somethingWentWrong, 0));
    } finally {
      setBusy(false);
    }
  }

  const request = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      await api.post("/auth/forgot-password", { identifier }, { noRefresh: true });
      toast.success(t.auth.ifTheAccountExistsA);
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
      toast.success(t.auth.passwordChangedSignInWith);
      navigate("/login", { replace: true });
    });
  };

  return (
    <AuthLayout>
      <div>
        <div className="mb-2 flex items-center justify-between gap-2">
          <h1 className="text-[22px] font-semibold tracking-tight text-[var(--od-ink)]">
            {t.auth.resetYourPassword}
          </h1>
          <Badge variant="secondary" className="od-tabular text-[11px]">
            {fmt(t.auth.stepOf2, { value: step === "request" ? "1" : "2" })}
          </Badge>
        </div>
        <p className="text-sm text-[var(--od-ink-secondary)]">
          {step === "request" ? t.auth.enterYourPhoneOrEmail : t.auth.enterTheCodeWeSent}
        </p>
      </div>

      <form onSubmit={step === "request" ? request : reset} className="mt-6 space-y-4" noValidate>
        <Field label={t.auth.phoneOrEmail} error={error?.fields.identifier}>
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
            <Field label={t.auth.n6DigitCode} error={error?.fields.code}>
              <Input
                required
                inputMode="numeric"
                maxLength={6}
                className="od-mono tracking-[0.2em]"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                autoComplete="one-time-code"
              />
            </Field>
            <Field label={t.auth.newPassword} error={error?.fields.new_password}>
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
          <div
            role="alert"
            className="flex items-start gap-2 rounded-xl border border-[var(--od-bad-mark)]/30 bg-[var(--od-bad-soft,rgba(239,68,68,0.08))] p-3 text-sm text-[var(--od-bad)]"
          >
            <AlertCircle size={16} className="mt-0.5 shrink-0" />
            <span>{error.message}</span>
          </div>
        )}
        <Button type="submit" className="w-full" disabled={busy}>
          {busy
            ? t.auth.pleaseWait
            : step === "request"
              ? t.auth.sendCode
              : t.common.changePassword}
        </Button>
      </form>

      <p className="mt-6 border-t border-[var(--od-border)] pt-5 text-center text-sm">
        <Link
          className="inline-flex items-center gap-1.5 font-medium text-[var(--od-ink-secondary)] transition hover:text-[var(--od-ink)]"
          to="/login"
        >
          <ArrowLeft size={14} /> {t.auth.backToSignIn}
        </Link>
      </p>
    </AuthLayout>
  );
}
