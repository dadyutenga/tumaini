import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useDebounce } from "@/hooks/useDebounce";
import { request } from "@/lib/api";
import { isDocumentNumber, normalizePhone } from "./query";
import { PER_GROUP, toSearchResult } from "./results";
import type { RecordKind, SearchResult } from "./types";

export const SEARCH_DELAY = 200;

export interface SearchRequest {
  text: string;
  /** One kind to search, when a chip narrows the search. */
  kind?: RecordKind;
  /** Search every branch instead of the one selected in the top bar. */
  everywhere?: boolean;
}

export async function fetchSearch({ text, kind, everywhere }: SearchRequest, signal?: AbortSignal) {
  const q = normalizePhone(text);
  const reply = await request<{ data: SearchResult }>("/search", {
    query: { q, scope: kind, limit: PER_GROUP },
    branch: everywhere ? "all" : undefined,
    signal,
  });
  return toSearchResult(reply.data);
}

/** Two characters are enough to ask the server; a document number is always enough. */
export const isSearchable = (text: string) => text.length >= 2 || isDocumentNumber(text);

/**
 * Live results. Typing is debounced, an outdated request is cancelled, and the previous
 * results stay on screen until the new ones arrive so the list does not jump.
 */
export function useSearch(params: SearchRequest, enabled: boolean) {
  const text = useDebounce(params.text, SEARCH_DELAY);
  const active = enabled && isSearchable(text) && isSearchable(params.text);
  const query = useQuery({
    queryKey: ["/search", text, params.kind ?? "", !!params.everywhere],
    queryFn: ({ signal }) => fetchSearch({ ...params, text }, signal),
    enabled: active,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    retry: false,
  });
  const waiting = enabled && isSearchable(params.text);
  return {
    result: waiting ? query.data : undefined,
    /** The results on screen belong to exactly what is typed. */
    settled: active && text === params.text && !query.isPlaceholderData && query.isSuccess,
    loading: waiting && (text !== params.text || query.isFetching),
    error: active && query.isError,
    retry: () => void query.refetch(),
  };
}
