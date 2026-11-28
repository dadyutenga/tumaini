import type { Locale } from "@/types";
import { dictionaries } from "./active";
import { fmt } from "./format";

/** The stock error names the product and shop, which must survive translation. */
const STOCK_SHORT = /^Not enough stock: (.+) at (.+) \(short by ([\d.]+)\)$/;

/**
 * An API error in the user's language. The server writes English. A message is translated when
 * it is one we know word for word, or when its code only ever means one thing. Codes such as
 * `invalid_body` cover many different messages, so those keep the server's own wording rather
 * than lose it.
 */
export function translateError(locale: Locale, code: string | undefined, message: string) {
  if (locale === "en") return message;
  const t = dictionaries[locale].errors;
  const stock = STOCK_SHORT.exec(message);
  if (stock)
    return fmt(t.stockShort, { product: stock[1], location: stock[2], quantity: stock[3] });
  const english = dictionaries.en.errors.messages;
  const known = (Object.keys(english) as (keyof typeof english)[]).find(
    (key) => english[key] === message,
  );
  if (known) return t.messages[known];
  const byCode = t.codes as Record<string, string>;
  if (code && Object.prototype.hasOwnProperty.call(byCode, code)) return byCode[code];
  return message;
}
