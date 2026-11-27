import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const tokensCss = fs.readFileSync(path.resolve(__dirname, "od-tokens.css"), "utf8");
const globalsCss = fs.readFileSync(path.resolve(__dirname, "globals.css"), "utf8");

function extractBlock(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = css.match(new RegExp(`(?:^|\\n)${escaped}\\s*\\{([\\s\\S]*?)\\n\\}`));
  if (!match) throw new Error(`Block not found for selector: ${selector}`);
  return match[1];
}

function parseVars(block: string): Record<string, string> {
  const vars: Record<string, string> = {};
  for (const m of block.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/gi)) {
    vars[m[1]] = m[2].trim();
  }
  return vars;
}

function hexToRgb(hex: string): [number, number, number] {
  const clean = hex.trim().replace(/^#/, "");
  const full =
    clean.length === 3
      ? clean
          .split("")
          .map((c) => c + c)
          .join("")
      : clean;
  if (!/^[0-9a-fA-F]{6}$/.test(full)) {
    throw new Error(`Expected 6-digit hex color, got: ${hex}`);
  }
  return [
    parseInt(full.slice(0, 2), 16),
    parseInt(full.slice(2, 4), 16),
    parseInt(full.slice(4, 6), 16),
  ];
}

function mixHex(fgHex: string, bgHex: string, fgWeight: number): string {
  const [fr, fg, fb] = hexToRgb(fgHex);
  const [br, bg, bb] = hexToRgb(bgHex);
  const r = Math.round(fr * fgWeight + br * (1 - fgWeight));
  const g = Math.round(fg * fgWeight + bg * (1 - fgWeight));
  const b = Math.round(fb * fgWeight + bb * (1 - fgWeight));
  return "#" + [r, g, b].map((n) => n.toString(16).padStart(2, "0")).join("");
}

function relativeLuminance(hex: string): number {
  const [r, g, b] = hexToRgb(hex).map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrastRatio(hexA: string, hexB: string): number {
  const l1 = relativeLuminance(hexA);
  const l2 = relativeLuminance(hexB);
  const [bright, dark] = l1 >= l2 ? [l1, l2] : [l2, l1];
  return (bright + 0.05) / (dark + 0.05);
}

describe("Soft light theme tokens & WCAG AA contrast", () => {
  const odLightBlock = extractBlock(tokensCss, ".od");
  const odAppLightBlock = extractBlock(tokensCss, ".od-app");
  const odDarkBlock = extractBlock(tokensCss, '.od[data-mode="dark"]');
  const rootLightBlock = extractBlock(globalsCss, ":root");

  const odLight = parseVars(odLightBlock);
  const odAppLight = parseVars(odAppLightBlock);
  const odDark = parseVars(odDarkBlock);

  it("declares color-scheme: light and eliminates pure white/black in soft light tokens", () => {
    expect(odLightBlock).toContain("color-scheme: light");
    expect(rootLightBlock).toContain("color-scheme: light");

    for (const [name, block] of [
      [".od", odLightBlock],
      [".od-app", odAppLightBlock],
      [":root", rootLightBlock],
    ] as const) {
      expect(block, `${name} must not contain pure white`).not.toMatch(
        /#fff(fff)?\b|oklch\(1 0 0\)/i,
      );
      expect(block, `${name} must not contain pure black`).not.toMatch(/#000(000)?\b/i);
    }
  });

  it("meets WCAG AA (>= 4.5:1) for all primary text, button, link, and muted pairs", () => {
    const page = odLight["--od-page"];
    const card = odLight["--od-surface"];
    const ink = odLight["--od-ink"];
    const muted = odLight["--od-ink-muted"];
    const primary = odAppLight["--od-app-primary"];
    const primaryFg = odAppLight["--od-app-primary-foreground"];
    const link = odLight["--od-link"];
    const ring = odAppLight["--od-app-ring"];

    expect(contrastRatio(ink, page)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(ink, card)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(muted, page)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(muted, card)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(primaryFg, primary)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(link, card)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(link, page)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(ring, card)).toBeGreaterThanOrEqual(3.0);
  });

  it("meets WCAG AA (>= 4.5:1) for desaturated status text on cards, soft badges, and solid pills", () => {
    const card = odLight["--od-surface"];
    const good = odLight["--od-good"];
    const goodMark = odLight["--od-good-mark"];
    const bad = odLight["--od-bad"];
    const badMark = odLight["--od-bad-mark"];
    const warning = odLight["--od-warning"];
    const warningSoft = odLight["--od-warning-soft"];
    const warningMark = odLight["--od-warning-mark"];
    const info = odLight["--od-info"];
    const onSolid = odLight["--od-badge-on-solid"];
    const onWarning = odLight["--od-badge-on-warning"];

    // Text on card surface
    expect(contrastRatio(good, card)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(bad, card)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(warning, card)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(info, card)).toBeGreaterThanOrEqual(4.5);

    // Text on soft 12% status badge backgrounds (StatusBadge)
    expect(contrastRatio(good, mixHex(goodMark, card, 0.12))).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(bad, mixHex(badMark, card, 0.12))).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(warning, warningSoft)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(info, mixHex(info, card, 0.12))).toBeGreaterThanOrEqual(4.5);

    // Text on solid status pills (notification badge, POS & Products stock badges)
    expect(contrastRatio(onSolid, good)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(onSolid, bad)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(onWarning, warningMark)).toBeGreaterThanOrEqual(4.5);
  });

  it("preserves dark mode palette unchanged", () => {
    expect(odDarkBlock).toContain("color-scheme: dark");
    expect(odDark["--od-page"]).toBe("#141414");
    expect(odDark["--od-surface"]).toBe("#151515");
    expect(odDark["--od-ink"]).toBe("#fafafa");
    expect(odDark["--od-good"]).toBe("#7fe0b1");
    expect(odDark["--od-bad"]).toBe("#f1838c");
    expect(odDark["--od-warning"]).toBe("#ffd84d");
    expect(odDark["--od-info"]).toBe("#8fbcf7");
  });
});
