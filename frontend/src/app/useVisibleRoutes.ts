import { useAuth } from "@/features/auth/auth";
import { routes } from "./routes";

/** Routes the signed-in user is allowed to open. */
export function useVisibleRoutes() {
  const { canAny } = useAuth();
  return routes.filter((route) => !route.permission || canAny(...route.permission));
}
