import { useEffect, useState } from "react";
import { useLocation, useSearchParams } from "react-router-dom";

/**
 * The text of a list page's search box. It starts from `?q=` in the address, so the search
 * palette and shared links open the list already filtered. `onArrive` lets a paged list go
 * back to its first page.
 */
export function useSearchBox(onArrive?: () => void) {
  const [params] = useSearchParams();
  const { key } = useLocation();
  const requested = params.get("q");
  const [text, setText] = useState(requested ?? "");

  // Arriving again while the page is already open replaces what was typed.
  useEffect(() => {
    if (requested === null) return;
    setText(requested);
    onArrive?.();
    // `onArrive` is a new function on every render; the address decides when this runs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, requested]);

  return [text, setText] as const;
}
