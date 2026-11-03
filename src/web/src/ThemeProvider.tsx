import { createContext, useContext, useState, type ReactNode } from "react";
export type Theme = "light" | "dark";
const key = "kraken-theme";
function saved(): Theme {
  try {
    const value = localStorage.getItem(key);
    if (value === "light" || value === "dark") return value;
  } catch {}
  return "light";
}
function apply(theme: Theme) {
  document.documentElement.classList.toggle("dark", theme === "dark");
}
apply(saved());
const Context = createContext<{
  theme: Theme;
  dark: boolean;
  setTheme: (theme: Theme) => void;
}>({ theme: "light", dark: false, setTheme: () => {} });
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, update] = useState<Theme>(saved);
  const [dark, setDark] = useState(() =>
    document.documentElement.classList.contains("dark"),
  );
  function setTheme(value: Theme) {
    update(value);
    apply(value);
    setDark(document.documentElement.classList.contains("dark"));
    try {
      localStorage.setItem(key, value);
    } catch {}
  }
  return <Context value={{ theme, dark, setTheme }}>{children}</Context>;
}
export const useTheme = () => useContext(Context);
