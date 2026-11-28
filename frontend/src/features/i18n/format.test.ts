import { describe, expect, it } from "vitest";
import { fmt, plural } from "./format";

describe("fmt", () => {
  it("fills every placeholder, including repeats and zero", () => {
    expect(fmt("{count} of {total} ({count})", { count: 0, total: 5 })).toBe("0 of 5 (0)");
  });

  it("leaves a placeholder with no value as written", () => {
    expect(fmt("Hello {name} {constructor}", {})).toBe("Hello {name} {constructor}");
  });

  it("does not expand placeholders inside a value", () => {
    expect(fmt("{a} {b}", { a: "{b}", b: "x" })).toBe("{b} x");
  });
});

describe("plural", () => {
  const forms = { one: "{count} item", other: "{count} items" };

  it.each(["en", "sw"] as const)("uses the singular only for one in %s", (locale) => {
    expect(plural(locale, 1, forms)).toBe(forms.one);
    for (const count of [0, 2, 11, 1.5]) expect(plural(locale, count, forms)).toBe(forms.other);
  });
});
