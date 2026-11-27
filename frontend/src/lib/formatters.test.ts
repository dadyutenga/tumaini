import { describe, it, expect } from "vitest";
import { formatBytes, formatCurrency, formatDate, formatNumber } from "./formatters";

describe("formatters", () => {
  it("formats numbers with locale separators", () => {
    expect(formatNumber(1250000, "en-US")).toBe("1,250,000");
  });

  it("formats currency accurately and defaults to active tenant currency", async () => {
    const { formatMoney, setActiveCurrency, getActiveCurrency } = await import("./formatters");
    expect(formatCurrency(49.99, "USD", "en-US")).toBe("$49.99");
    expect(getActiveCurrency()).toBe("TZS");
    expect(formatMoney("2500.5")).toBe("TZS 2,500.50");
    setActiveCurrency("KES");
    expect(formatMoney("1000")).toBe("KES 1,000.00");
    expect(formatCurrency(1000)).toContain("1,000");
    setActiveCurrency("TZS");
  });

  it("formats date in medium dateStyle", () => {
    const formatted = formatDate("2026-04-15T00:00:00Z", "en-US");
    expect(formatted).toContain("2026");
    expect(formatted).toContain("Apr");
  });

  it("formats bytes into human readable units", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1024)).toBe("1 KB");
    expect(formatBytes(1048576)).toBe("1 MB");
    expect(formatBytes(1073741824)).toBe("1 GB");
    expect(formatBytes(-1)).toBe("—");
  });
});

describe("decimal helpers", () => {
  it("reads strings, numbers and nullable decimals", async () => {
    const { toNumber, formatQty } = await import("./formatters");
    expect(toNumber("2500.50")).toBe(2500.5);
    expect(toNumber(7)).toBe(7);
    expect(toNumber(null)).toBe(0);
    expect(toNumber({ Decimal: "12", Valid: true })).toBe(12);
    expect(formatQty("1234.5")).toBe("1,234.5");
  });
});
