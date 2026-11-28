import { useAuth } from "@/features/auth/auth";
import { useBusiness } from "@/features/business/useBusiness";
import { routes } from "./routes";

/** Routes the signed-in user is allowed to open. */
export function useVisibleRoutes() {
  const { canAny } = useAuth();
  const { works } = useBusiness();
  return routes.filter(
    (route) => (!route.permission || canAny(...route.permission)) && works(route.business),
  );
}
