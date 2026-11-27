import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, get, refreshSession, session } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Me } from "@/types";

type Status = "loading" | "authed" | "anon";

interface Credentials {
  identifier: string;
  password: string;
}
export interface RegisterInput {
  business_name: string;
  owner_name: string;
  phone: string;
  email: string;
  password: string;
}
interface SessionResult {
  access_token?: string;
  branch_id?: string;
  must_change_password?: boolean;
  mfa_required?: boolean;
  mfa_token?: string;
}

export interface LoginOutcome {
  mfaRequired: boolean;
  mfaToken?: string;
}

interface Auth {
  status: Status;
  me: Me | null;
  /** Kept for components that only need the signed-in user. */
  user: Me["user"] | null;
  branchId: string | null;
  setBranchId: (id: string) => void;
  can: (permission: string) => boolean;
  canAny: (...permissions: string[]) => boolean;
  limit: (code: string) => string | null;
  login: (input: Credentials) => Promise<LoginOutcome>;
  verifyMfa: (mfaToken: string, code: string) => Promise<void>;
  register: (input: RegisterInput) => Promise<void>;
  logout: () => Promise<void>;
  reloadMe: () => Promise<void>;
}

const Context = createContext<Auth | null>(null);
const BRANCH_KEY = "bms-branch";
const IDLE_TIMEOUT_MS = 30 * 60 * 1000;
const WARN_BEFORE_MS = 60 * 1000;

function storedBranch() {
  try {
    return localStorage.getItem(BRANCH_KEY);
  } catch {
    return null;
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<Status>("loading");
  const [me, setMe] = useState<Me | null>(null);
  const [branchId, setBranch] = useState<string | null>(storedBranch);
  const [warnSeconds, setWarnSeconds] = useState<number | null>(null);
  const [reauthOpen, setReauthOpen] = useState(false);
  const [reauthPassword, setReauthPassword] = useState("");
  const [reauthCode, setReauthCode] = useState("");
  const [reauthBusy, setReauthBusy] = useState(false);
  const [reauthError, setReauthError] = useState<string | null>(null);
  const reauthResolverRef = useRef<((ok: boolean) => void) | null>(null);
  const lastActiveRef = useRef(Date.now());

  const loadMe = useCallback(
    async (defaultBranch?: string) => {
      const profile = await get<Me>("/me");
      const allowed = profile.branches ?? [];
      let next = branchId || defaultBranch || allowed[0] || null;
      if (!profile.is_owner && allowed.length === 1 && next && next !== allowed[0]) {
        next = allowed[0];
      }
      session.setBranch(next);
      setBranch(next);
      setMe(profile);
      setStatus("authed");
    },
    [branchId],
  );

  const clear = useCallback(() => {
    session.setToken(null);
    session.setBranch(null);
    setMe(null);
    setWarnSeconds(null);
    setStatus("anon");
    queryClient.clear();
  }, [queryClient]);

  useEffect(() => {
    session.onLost(clear);
    session.onReauthRequired(
      () =>
        new Promise<boolean>((resolve) => {
          reauthResolverRef.current = resolve;
          setReauthPassword("");
          setReauthCode("");
          setReauthError(null);
          setReauthOpen(true);
        }),
    );
    return () => {
      session.onLost(null);
      session.onReauthRequired(null);
    };
  }, [clear]);

  const closeReauth = useCallback((ok: boolean) => {
    setReauthOpen(false);
    setReauthPassword("");
    setReauthCode("");
    setReauthError(null);
    const resolve = reauthResolverRef.current;
    reauthResolverRef.current = null;
    resolve?.(ok);
  }, []);

  const submitReauth = useCallback(
    async (e: FormEvent) => {
      e.preventDefault();
      setReauthBusy(true);
      setReauthError(null);
      try {
        await api.post(
          "/auth/reauthenticate",
          { password: reauthPassword, mfa_code: reauthCode || undefined },
          { noRefresh: true },
        );
        closeReauth(true);
      } catch (err) {
        setReauthError(err instanceof ApiError ? err.message : "Could not verify your identity.");
      } finally {
        setReauthBusy(false);
      }
    },
    [reauthPassword, reauthCode, closeReauth],
  );

  useEffect(() => {
    let cancelled = false;
    session.setBranch(branchId);
    void (async () => {
      if (!(await refreshSession())) {
        if (!cancelled) setStatus("anon");
        return;
      }
      try {
        await loadMe();
      } catch {
        if (!cancelled) clear();
      }
    })();
    return () => {
      cancelled = true;
    };
    // Run once on start: restore the session from the refresh cookie.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const enter = useCallback(
    async (result: SessionResult) => {
      session.setToken(result.access_token ?? null);
      lastActiveRef.current = Date.now();
      await loadMe(result.branch_id);
    },
    [loadMe],
  );

  const login = useCallback(
    async (input: Credentials): Promise<LoginOutcome> => {
      const result = await api.post<SessionResult>("/auth/login", input, { noRefresh: true });
      if (result.mfa_required && result.mfa_token) {
        return { mfaRequired: true, mfaToken: result.mfa_token };
      }
      await enter(result);
      return { mfaRequired: false };
    },
    [enter],
  );

  const verifyMfa = useCallback(
    async (mfaToken: string, code: string) => {
      const result = await api.post<SessionResult>(
        "/auth/mfa/verify",
        { mfa_token: mfaToken, code },
        { noRefresh: true },
      );
      await enter(result);
    },
    [enter],
  );

  const register = useCallback(
    async (input: RegisterInput) => {
      const result = await api.post<SessionResult>("/auth/register", input, { noRefresh: true });
      await enter(result);
    },
    [enter],
  );

  const logout = useCallback(async () => {
    try {
      await api.post("/auth/logout");
    } catch (error) {
      if (!(error instanceof ApiError)) throw error;
    }
    clear();
  }, [clear]);

  const reloadMe = useCallback(async () => {
    await loadMe();
  }, [loadMe]);

  // SRS Section 4: auto sign out after 30 minutes of inactivity, with a warning 1 minute before.
  useEffect(() => {
    if (status !== "authed") return;
    lastActiveRef.current = Date.now();
    const touch = () => {
      if (Date.now() - lastActiveRef.current < IDLE_TIMEOUT_MS - WARN_BEFORE_MS) {
        lastActiveRef.current = Date.now();
      }
    };
    const events: (keyof WindowEventMap)[] = ["pointerdown", "keydown", "scroll", "touchstart"];
    for (const ev of events) window.addEventListener(ev, touch, { passive: true });
    const timer = window.setInterval(() => {
      const elapsed = Date.now() - lastActiveRef.current;
      if (elapsed >= IDLE_TIMEOUT_MS) {
        setWarnSeconds(null);
        void logout();
      } else if (elapsed >= IDLE_TIMEOUT_MS - WARN_BEFORE_MS) {
        setWarnSeconds(Math.max(1, Math.ceil((IDLE_TIMEOUT_MS - elapsed) / 1000)));
      } else {
        setWarnSeconds(null);
      }
    }, 1000);
    return () => {
      for (const ev of events) window.removeEventListener(ev, touch);
      window.clearInterval(timer);
    };
  }, [status, logout]);

  const setBranchId = useCallback(
    (id: string) => {
      session.setBranch(id);
      setBranch(id);
      try {
        localStorage.setItem(BRANCH_KEY, id);
      } catch {
        /* storage may be unavailable */
      }
      void queryClient.invalidateQueries();
    },
    [queryClient],
  );

  const value = useMemo<Auth>(() => {
    const perms = me?.permissions ?? {};
    const limits = me?.limits ?? {};
    const can = (permission: string) => Boolean(me?.is_owner || perms[permission]);
    const limit = (code: string) => limits[code] ?? null;
    return {
      status,
      me,
      user: me?.user ?? null,
      branchId,
      setBranchId,
      can,
      canAny: (...permissions: string[]) => permissions.some(can),
      limit,
      login,
      verifyMfa,
      register,
      logout,
      reloadMe,
    };
  }, [status, me, branchId, setBranchId, login, verifyMfa, register, logout, reloadMe]);

  return (
    <Context.Provider value={value}>
      {children}
      <Dialog open={reauthOpen} onOpenChange={(o) => !o && closeReauth(false)}>
        <DialogContent>
          <form onSubmit={submitReauth} className="space-y-4">
            <DialogHeader>
              <DialogTitle>Confirm your identity</DialogTitle>
              <DialogDescription>
                This sensitive account change requires confirming your password
                {me?.mfa_enabled ? " and authenticator code" : ""}.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-1">
              <label className="text-sm font-medium">Current password *</label>
              <Input
                required
                autoFocus
                type="password"
                autoComplete="current-password"
                value={reauthPassword}
                onChange={(e) => setReauthPassword(e.target.value)}
              />
            </div>
            {me?.mfa_enabled && (
              <div className="space-y-1">
                <label className="text-sm font-medium">Authenticator or recovery code *</label>
                <Input
                  required
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  placeholder="6-digit code or XXXX-XXXX"
                  value={reauthCode}
                  onChange={(e) => setReauthCode(e.target.value)}
                />
              </div>
            )}
            {reauthError && (
              <p role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {reauthError}
              </p>
            )}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => closeReauth(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={reauthBusy}>
                {reauthBusy ? "Verifying…" : "Confirm"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={warnSeconds !== null} onOpenChange={() => {}}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Session expiring soon</DialogTitle>
            <DialogDescription>
              You have been inactive for 29 minutes. You will be signed out in {warnSeconds ?? 60}{" "}
              seconds. Any POS sale in progress is saved as a draft.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => void logout()}>
              Sign out now
            </Button>
            <Button
              onClick={() => {
                lastActiveRef.current = Date.now();
                setWarnSeconds(null);
              }}
            >
              Stay signed in
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Context.Provider>
  );
}

export function useAuth() {
  const value = useContext(Context);
  if (!value) throw new Error("AuthProvider is missing");
  return value;
}

function FirstLoginPasswordScreen() {
  const { user, reloadMe, logout } = useAuth();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (newPassword !== confirm) {
      setError("New passwords do not match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.put("/auth/password", {
        current_password: currentPassword,
        new_password: newPassword,
      });
      await reloadMe();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not update password.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid min-h-screen place-items-center bg-background p-4">
      <form
        onSubmit={submit}
        className="w-full max-w-md space-y-4 rounded-2xl border bg-card p-6 shadow-[var(--od-shadow)]"
      >
        <div>
          <h1 className="text-xl font-semibold">Change your temporary password</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Welcome, {user?.name}. Before continuing, choose a personal password of at least 8
            characters.
          </p>
        </div>
        <div className="space-y-1">
          <label className="text-sm font-medium">Temporary password *</label>
          <Input
            required
            type="password"
            autoComplete="current-password"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <label className="text-sm font-medium">New password *</label>
          <Input
            required
            minLength={8}
            type="password"
            autoComplete="new-password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <label className="text-sm font-medium">Confirm new password *</label>
          <Input
            required
            minLength={8}
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </div>
        {error && (
          <p role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
            {error}
          </p>
        )}
        <div className="flex items-center justify-between gap-2 pt-2">
          <Button type="button" variant="ghost" onClick={() => void logout()}>
            Sign out
          </Button>
          <Button type="submit" disabled={busy}>
            {busy ? "Saving…" : "Update password"}
          </Button>
        </div>
      </form>
    </div>
  );
}

export function ProtectedRoute({
  children,
  permission,
}: {
  children: ReactNode;
  permission?: string | string[];
}) {
  const { status, user, can } = useAuth();
  const location = useLocation();
  if (status === "loading")
    return (
      <div className="grid min-h-screen place-items-center text-sm text-muted-foreground">
        Loading…
      </div>
    );
  if (status === "anon")
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  if (user?.must_change_password) return <FirstLoginPasswordScreen />;
  const needed =
    permission === undefined ? [] : Array.isArray(permission) ? permission : [permission];
  if (needed.length > 0 && !needed.some(can)) return <Navigate to="/403" replace />;
  return children;
}
