import { useRef } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  ApiError,
  api,
  get,
  getPage,
  isPendingApproval,
  newIdempotencyKey,
  request,
} from "@/lib/api";
import { useI18n } from "@/features/i18n/i18n";
import { activeT } from "@/features/i18n/active";

type Query = Record<string, string | number | boolean | undefined | null>;

export function useGet<T>(
  path: string,
  query?: Query,
  /** `branch` asks about another branch than the one in the top bar, or "all" of them. */
  options: { enabled?: boolean; branch?: string } = {},
) {
  const { branch } = options;
  return useQuery({
    queryKey: branch ? [path, query ?? {}, branch] : [path, query ?? {}],
    queryFn: ({ signal }) =>
      branch
        ? request<{ data: T }>(path, { query, signal, branch }).then((reply) => reply.data)
        : get<T>(path, query, signal),
    enabled: options.enabled,
    staleTime: 15_000,
  });
}

export function useGetPage<T>(path: string, query?: Query, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: [path, "page", query ?? {}],
    queryFn: ({ signal }) => getPage<T>(path, query, signal),
    enabled: options.enabled,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
  });
}

export function errorMessage(error: unknown) {
  if (error instanceof ApiError) {
    const fields = Object.values(error.fields);
    return fields.length ? `${error.message}: ${fields.join(", ")}` : error.message;
  }
  return error instanceof Error ? error.message : activeT().common.somethingWentWrong;
}

interface MutateOptions<R, B = unknown> {
  /** Query keys (path prefixes) to refresh afterwards. */
  invalidate?: string[];
  success?: string;
  idempotent?: boolean;
  onSuccess?: (result: R) => void;
  onError?: (error: unknown, variables: B) => void;
}

interface BodyOption<B> {
  /** Map the mutation variables to the JSON body. Use it when the variables also carry the target id. */
  toBody?: (variables: B) => unknown;
}

/** POST, PUT, or DELETE with toast feedback, approval handling, and retry-safe idempotency keys. */
export function useWrite<B = unknown, R = unknown>(
  method: "POST" | "PUT" | "PATCH" | "DELETE",
  path: string | ((body: B) => string),
  options: MutateOptions<R, B> & BodyOption<B> = {},
) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  // Keep the same Idempotency-Key across network/5xx retries of the exact same request so a
  // dropped response never results in duplicate financial postings when the user retries.
  const pendingKey = useRef<{ sig: string; key: string } | null>(null);
  return useMutation({
    mutationFn: (body: B) => {
      const target = typeof path === "function" ? path(body) : path;
      const payload = options.toBody ? options.toBody(body) : body;
      let key: string | undefined;
      if (options.idempotent) {
        const sig = `${method}:${target}:${JSON.stringify(payload ?? null)}`;
        if (pendingKey.current?.sig === sig) {
          key = pendingKey.current.key;
        } else {
          key = newIdempotencyKey();
          pendingKey.current = { sig, key };
        }
      }
      if (method === "POST") return api.post<R>(target, payload, { idempotencyKey: key });
      if (method === "PUT") return api.put<R>(target, payload);
      if (method === "PATCH") return api.patch<R>(target, payload);
      return api.del<R>(target);
    },
    onSuccess: (result) => {
      pendingKey.current = null;
      if (isPendingApproval(result)) {
        toast.info(t.common.sentForApprovalAManager);
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
    onError: (error, variables) => {
      // Clear key on deterministic 4xx errors (server rolled back), but retain it on network
      // timeouts (status 0) or 5xx responses where the write may have committed on the server.
      if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
        pendingKey.current = null;
      }
      toast.error(errorMessage(error));
      options.onError?.(error, variables);
    },
  });
}
