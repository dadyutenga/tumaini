import { beforeEach, describe, it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { I18nProvider, useI18n } from "./i18n";
import en from "./locales/en";
import sw from "./locales/sw";

function TestConsumer() {
  const { locale, setLocale, t } = useI18n();
  return (
    <div>
      <span data-testid="locale">{locale}</span>
      <span data-testid="title">{t.settings.title}</span>
      <button onClick={() => setLocale("sw")}>Set Swahili</button>
      <button onClick={() => setLocale("en")}>Set English</button>
    </div>
  );
}

/** Every string in a dictionary, keyed by its dotted path. */
function flatten(node: object, prefix = ""): [string, string][] {
  return Object.entries(node).flatMap(([key, value]) =>
    typeof value === "string"
      ? [[prefix + key, value] as [string, string]]
      : flatten(value as object, `${prefix}${key}.`),
  );
}
const placeholders = (text: string) => (text.match(/\{\w+\}/g) ?? []).sort();

describe("i18n", () => {
  beforeEach(() => localStorage.clear());

  it("has the same keys and placeholders in both languages", () => {
    const swahili = new Map(flatten(sw));
    expect([...swahili.keys()].sort()).toEqual(
      flatten(en)
        .map(([key]) => key)
        .sort(),
    );
    for (const [key, text] of flatten(en)) {
      expect(placeholders(swahili.get(key) ?? ""), key).toEqual(placeholders(text));
      expect(swahili.get(key)?.trim(), key).toBeTruthy();
    }
  });

  it("moves a language saved under the old key to bms-locale", () => {
    localStorage.setItem("template-locale", "sw");
    render(
      <I18nProvider>
        <TestConsumer />
      </I18nProvider>,
    );
    expect(screen.getByTestId("locale")).toHaveTextContent("sw");
    expect(localStorage.getItem("bms-locale")).toBe("sw");
    expect(localStorage.getItem("template-locale")).toBeNull();
  });

  it("keeps the newer choice when both keys exist", () => {
    localStorage.setItem("bms-locale", "en");
    localStorage.setItem("template-locale", "sw");
    render(
      <I18nProvider>
        <TestConsumer />
      </I18nProvider>,
    );
    expect(screen.getByTestId("locale")).toHaveTextContent("en");
    expect(localStorage.getItem("template-locale")).toBeNull();
  });

  it("defaults to English and supports switching to Swahili", () => {
    render(
      <I18nProvider>
        <TestConsumer />
      </I18nProvider>,
    );

    expect(screen.getByTestId("locale")).toHaveTextContent("en");
    expect(screen.getByTestId("title")).toHaveTextContent("Settings");

    fireEvent.click(screen.getByText("Set Swahili"));
    expect(screen.getByTestId("locale")).toHaveTextContent("sw");
    expect(screen.getByTestId("title")).toHaveTextContent("Mipangilio");
  });

  it("provides safe fallback when rendered outside I18nProvider", () => {
    render(<TestConsumer />);
    expect(screen.getByTestId("locale")).toHaveTextContent("en");
    expect(screen.getByTestId("title")).toHaveTextContent("Settings");
  });
});
