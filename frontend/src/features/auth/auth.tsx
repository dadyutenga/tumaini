import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, get, refreshSession, session } from "@/lib/api";
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
  access_token: string;
  branch_id: string;
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
  login: (input: Credentials) => Promise<void>;
  register: (input: RegisterInput) => Promise<void>;
  logout: () => Promise<void>;
}

const Context = createContext<Auth | null>(null);
const BRANCH_KEY = "tumaini-branch";

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

  const loadMe = useCallback(
    async (defaultBranch?: string) => {
      const profile = await get<Me>("/me");
      const allowed = profile.branches ?? [];
      const wanted = [branchId, defaultBranch].find((id) => id && allowed.includes(id));
      const next = wanted ?? allowed[0] ?? null;
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
    setStatus("anon");
    queryClient.clear();
  }, [queryClient]);

  useEffect(() => {
    session.onLost(clear);
    return () => session.onLost(null);
  }, [clear]);

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
      session.setToken(result.access_token);
      await loadMe(result.branch_id);
    },
    [loadMe],
  );

  const login = useCallback(
    async (input: Credentials) => {
      const result = await api.post<SessionResult>("/auth/login", input, { noRefresh: true });
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
    const can = (permission: string) => Boolean(me?.is_owner || perms[permission]);
    return {
      status,
      me,
      user: me?.user ?? null,
      branchId,
      setBranchId,
      can,
      canAny: (...permissions: string[]) => permissions.some(can),
      login,
      register,
      logout,
    };
  }, [status, me, branchId, setBranchId, login, register, logout]);

  return <Context.Provider value={value}>{children}</Context.Provider>;
}

export function useAuth() {
  const value = useContext(Context);
  if (!value) throw new Error("AuthProvider is missing");
  return value;
}

export function ProtectedRoute({
  children,
  permission,
}: {
  children: ReactNode;
  permission?: string | string[];
}) {
  const { status, can } = useAuth();
  const location = useLocation();
  if (status === "loading")
    return (
      <div className="grid min-h-screen place-items-center text-sm text-muted-foreground">
        Loading…
      </div>
    );
  if (status === "anon")
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  const needed =
    permission === undefined ? [] : Array.isArray(permission) ? permission : [permission];
  if (needed.length > 0 && !needed.some(can)) return <Navigate to="/403" replace />;
  return children;
}
