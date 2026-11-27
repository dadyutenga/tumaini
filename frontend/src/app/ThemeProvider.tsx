import { useEffect, type ReactNode } from "react";

function applyTheme(theme: string) {
  const dark =
    theme === "dark" || (theme === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  const highContrast = !dark && theme === "high-contrast";
  const root = document.documentElement;
  root.classList.add("od", "od-app");
  root.classList.toggle("dark", dark);
  root.setAttribute("data-mode", dark ? "dark" : "light");
  if (highContrast) {
    root.setAttribute("data-contrast", "high");
  } else {
    root.removeAttribute("data-contrast");
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const update = () => applyTheme(localStorage.getItem("theme") ?? "system");
    update();
    media.addEventListener("change", update);
    window.addEventListener("themechange", update);
    return () => {
      media.removeEventListener("change", update);
      window.removeEventListener("themechange", update);
    };
  }, []);
  return children;
}
