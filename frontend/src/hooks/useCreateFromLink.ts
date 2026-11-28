import { useEffect } from "react";
import { useSearchParams } from "react-router-dom";

/**
 * Opens a page's "new record" form when the address asks for it (`?new=1&name=…`), once.
 * `allowed` is false while the person may not create, or the form cannot be filled in yet.
 */
export function useCreateFromLink(allowed: boolean, open: (name: string) => void) {
  const [params, setParams] = useSearchParams();
  const asked = params.get("new") === "1";

  useEffect(() => {
    if (!asked || !allowed) return;
    open(params.get("name") ?? "");
    const rest = new URLSearchParams(params);
    rest.delete("new");
    rest.delete("name");
    setParams(rest, { replace: true });
    // `open` is a new function on every render; the address decides when this runs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [asked, allowed]);
}
