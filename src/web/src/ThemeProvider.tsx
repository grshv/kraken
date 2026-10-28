import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
export type Theme = "system" | "light" | "dark";
const key = "squid-control-theme";
function saved(): Theme {
  try {
    const value = localStorage.getItem(key);
    if (value === "light" || value === "dark") return value;
  } catch {}
  return "system";
}
function apply(theme: Theme) {
  document.documentElement.classList.toggle(
    "dark",
    theme === "dark" ||
      (theme === "system" &&
        matchMedia("(prefers-color-scheme: dark)").matches),
  );
}
apply(saved());
const Context = createContext<{
  theme: Theme;
  dark: boolean;
  setTheme: (theme: Theme) => void;
}>({ theme: "system", dark: false, setTheme: () => {} });
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, update] = useState<Theme>(saved);
  const [dark, setDark] = useState(() =>
    document.documentElement.classList.contains("dark"),
  );
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const sync = () => {
      apply(theme);
      setDark(document.documentElement.classList.contains("dark"));
    };
    sync();
    media.addEventListener("change", sync);
    return () => media.removeEventListener("change", sync);
  }, [theme]);
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
