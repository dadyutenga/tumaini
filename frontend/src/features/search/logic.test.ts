import { describe, expect, it } from "vitest";
import { isDocumentNumber, looksLikeBarcode, normalizePhone, parseQuery } from "./query";
import { addRecent, readRecent, removeRecent } from "./recent";
import { isExactMatch, listLink, toSearchResult } from "./results";
import type { SearchHit, SearchResult } from "./types";

describe("normalizePhone", () => {
  it.each(["0712345678", "712345678", "+255712345678", "255712345678", "0712 345-678"])(
    "reads %s as one number",
    (typed) => expect(normalizePhone(typed)).toBe("+255712345678"),
  );

  it("completes the start of a number typed with a leading 0 or the country code", () => {
    expect(normalizePhone("0712")).toBe("+255712");
    expect(normalizePhone("+255 71")).toBe("+25571");
  });

  it.each(["6001234567890", "7123", "R-0001", "Sukari 1kg", "0111222", "12345"])(
    "leaves %s alone",
    (typed) => expect(normalizePhone(typed)).toBe(typed),
  );
});

describe("typed input", () => {
  it("reads the > and # prefixes", () => {
    expect(parseQuery("> new")).toEqual({ mode: "actions", text: "new" });
    expect(parseQuery("#R-00")).toEqual({ mode: "documents", text: "R-00" });
    expect(parseQuery("  sukari ")).toEqual({ mode: "all", text: "sukari" });
  });

  it("recognises document numbers and scanned codes", () => {
    for (const code of ["R-0001", "INV-0012", "PRJ-0001"])
      expect(isDocumentNumber(code)).toBe(true);
    expect(isDocumentNumber("sukari")).toBe(false);
    expect(looksLikeBarcode("6001234567890")).toBe(true);
    expect(looksLikeBarcode("sukari 1kg")).toBe(false);
  });
});

const hit = (over: Partial<SearchHit>): SearchHit => ({
  kind: "sale",
  id: "1",
  title: "R-0001",
  subtitle: "",
  link: "/sales/1",
  ...over,
});

describe("search replies", () => {
  it("keeps the grouped reply and survives an empty one", () => {
    const grouped = { best: null, groups: [{ kind: "sale", total: 9, hits: [hit({})] }] };
    expect(toSearchResult(grouped as SearchResult)).toEqual(grouped);
    expect(toSearchResult(null)).toEqual({ best: null, groups: [] });
  });

  it("matches a product only on its own code", () => {
    const product = hit({ kind: "product", title: "Sugar", meta: { barcode: "600123" } });
    expect(isExactMatch(product, "600123")).toBe(true);
    expect(isExactMatch(product, "60012")).toBe(false);
    expect(isExactMatch(product, "Sugar")).toBe(false);
  });

  it("builds a safe link to the full list", () => {
    expect(listLink("service_invoice", "a&b =1")).toBe("/service-invoices?q=a%26b%20%3D1");
  });
});

describe("recent items", () => {
  const item = (n: number) => ({ type: "search", text: `q${n}`, link: `/sales?q=${n}` }) as const;

  it("keeps five, newest first, without repeats, per user", () => {
    for (let n = 1; n <= 7; n++) addRecent("u1", item(n));
    addRecent("u1", item(5));
    expect(readRecent("u1").map((r) => r.link)).toEqual([5, 7, 6, 4, 3].map((n) => item(n).link));
    expect(readRecent("u2")).toEqual([]);
    expect(removeRecent("u1", item(5).link)).toHaveLength(4);
  });

  it("ignores damaged storage and links that leave the app", () => {
    localStorage.setItem("bms-search-recent:u1", "{not json");
    expect(readRecent("u1")).toEqual([]);
    localStorage.setItem(
      "bms-search-recent:u1",
      JSON.stringify([
        { type: "search", text: "x", link: "https://example.com" },
        { type: "search", text: "x", link: "//example.com" },
        { type: "record", link: "/sales/1" },
        null,
        item(1),
      ]),
    );
    expect(readRecent("u1")).toEqual([item(1)]);
  });
});
