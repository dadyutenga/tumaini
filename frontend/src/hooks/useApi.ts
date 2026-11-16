import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ApiError, api, get, getPage, isPendingApproval, newIdempotencyKey } from "@/lib/api";

type Query = Record<string, string | number | boolean | undefined | null>;

export function useGet<T>(path: string, query?: Query, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: [path, query ?? {}],
    queryFn: ({ signal }) => get<T>(path, query, signal),
    enabled: options.enabled,
    staleTime: 15_000,
  });
}

export function useGetPage<T>(path: string, query?: Query) {
  return useQuery({
    queryKey: [path, "page", query ?? {}],
    queryFn: ({ signal }) => getPage<T>(path, query, signal),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
  });
}

export function errorMessage(error: unknown) {
  if (error instanceof ApiError) {
    const fields = Object.values(error.fields);
    return fields.length ? `${error.message}: ${fields.join(", ")}` : error.message;
  }
  return error instanceof Error ? error.message : "Something went wrong";
}

interface MutateOptions<R> {
  /** Query keys (path prefixes) to refresh afterwards. */
  invalidate?: string[];
  success?: string;
  idempotent?: boolean;
  onSuccess?: (result: R) => void;
}

/** POST, PUT, or DELETE with toast feedback, approval handling, and idempotency keys. */
export function useWrite<B = unknown, R = unknown>(
  method: "POST" | "PUT" | "DELETE",
  path: string | ((body: B) => string),
  options: MutateOptions<R> = {},
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: B) => {
      const target = typeof path === "function" ? path(body) : path;
      const key = options.idempotent ? newIdempotencyKey() : undefined;
      if (method === "POST") return api.post<R>(target, body, { idempotencyKey: key });
      if (method === "PUT") return api.put<R>(target, body);
      return api.del<R>(target);
    },
    onSuccess: (result) => {
      if (isPendingApproval(result)) {
        toast.info("Sent for approval. A manager must approve it before it completes.");
      } else if (options.success) {
        toast.success(options.success);
      }
      for (const key of options.invalidate ?? []) {
        void queryClient.invalidateQueries({
          predicate: (q) => String(q.queryKey[0]).startsWith(key),
        });
      }
      options.onSuccess?.(result);
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
}
