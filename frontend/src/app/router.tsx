import { Suspense } from "react";
import { createBrowserRouter } from "react-router-dom";
import DashboardLayout from "@/components/layout/DashboardLayout";
import { ProtectedRoute } from "@/features/auth/auth";
import { routes } from "./routes";
import AuthPage from "@/pages/Auth";
import ForgotPassword from "@/pages/ForgotPassword";
import ClientApproval from "@/pages/ClientApproval";
import Status from "@/pages/Status";
import { PageLoader } from "@/components/shared/Display";
import { ErrorBoundary, NotFound } from "@/components/shared/RouteErrors";

export const router = createBrowserRouter([
  {
    path: "/",
    element: (
      <ProtectedRoute>
        <DashboardLayout />
      </ProtectedRoute>
    ),
    errorElement: <ErrorBoundary />,
    children: routes.map(({ path, component: Component, permission }) => ({
      index: path === "/" ? true : undefined,
      path: path === "/" ? undefined : path.slice(1),
      element: (
        <ProtectedRoute permission={permission}>
          <Suspense fallback={<PageLoader />}>
            <Component />
          </Suspense>
        </ProtectedRoute>
      ),
    })),
  },
  ...["login", "register"].map((path) => ({
    path,
    element: <AuthPage mode={path} />,
  })),
  { path: "forgot-password", element: <ForgotPassword /> },
  // Private link the client opens from an SMS to approve a design. No sign-in.
  { path: "approve/:token", element: <ClientApproval /> },
  {
    path: "403",
    element: (
      <Status
        code="403"
        title="Access denied"
        description="Your role does not include this page."
      />
    ),
  },
  { path: "404", element: <Status code="404" title="Page not found" /> },
  { path: "500", element: <Status code="500" title="Server error" /> },
  { path: "*", element: <NotFound /> },
]);
