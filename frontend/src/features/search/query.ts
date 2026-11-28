/** What the user typed, taken apart: the mode prefix and the text to search for. */
export interface ParsedQuery {
  /** `>` asks for actions only, `#` for document numbers only. */
  mode: "all" | "actions" | "documents";
  text: string;
}

export function parseQuery(input: string): ParsedQuery {
  const value = input.trimStart();
  if (value.startsWith(">")) return { mode: "actions", text: value.slice(1).trim() };
  if (value.startsWith("#")) return { mode: "documents", text: value.slice(1).trim() };
  return { mode: "all", text: value.trim() };
}

/**
 * Writes a Tanzanian mobile number one way (`+255712345678`) so that `0712…`, `712…`,
 * `255712…` and `+255712…` find the same record. Anything else, including barcodes and
 * document numbers, is returned unchanged.
 */
export function normalizePhone(text: string): string {
  const compact = text.replace(/[\s().-]/g, "");
  const match =
    /^\+?255([67]\d{0,8})$/.exec(compact) ??
    // With a leading 0, accept the start of a number; without it, only a whole one.
    /^0([67]\d{2,8})$/.exec(compact) ??
    /^([67]\d{8})$/.exec(compact);
  return match ? `+255${match[1]}` : text;
}

/** Receipt, invoice, quotation, project and job numbers: letters, a dash, digits. */
export const isDocumentNumber = (text: string) => /^[A-Za-z]{1,4}-\d{2,}$/.test(text.trim());

/** What a scanner sends: one run of letters and digits with at least four digits. */
export const looksLikeBarcode = (text: string) =>
  /^[A-Za-z0-9-]{4,}$/.test(text) && (text.match(/\d/g)?.length ?? 0) >= 4;
