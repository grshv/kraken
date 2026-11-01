import { createContext, useContext, useState, type ReactNode } from "react";
export type Theme = "light" | "dark";
const key = "kraken-theme";
const variantKey = "kraken-theme-variant";
// Structural colour themes, picked in the Мок menu.
export const themeVariants = [
  { value: "base", label: "1. Графит / синий" },
  { value: "white", label: "2. Белая" },
  { value: "dark-list", label: "3. Тёмный список" },
  { value: "cards", label: "4. Карточки на сером" },
  { value: "mono", label: "5. Монохром" },
];
function savedVariant(): string {
  try {
    const value = localStorage.getItem(variantKey);
    if (value && themeVariants.some((v) => v.value === value)) return value;
  } catch {}
  return "base";
}
function applyVariant(variant: string) {
  document.documentElement.dataset.variant = variant;
}
applyVariant(savedVariant());
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
  variant: string;
  setVariant: (variant: string) => void;
}>({
  theme: "light",
  dark: false,
  setTheme: () => {},
  variant: "base",
  setVariant: () => {},
});
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
  const [variant, updateVariant] = useState(savedVariant);
  function setVariant(value: string) {
    updateVariant(value);
    applyVariant(value);
    try {
      localStorage.setItem(variantKey, value);
    } catch {}
  }
  return (
    <Context value={{ theme, dark, setTheme, variant, setVariant }}>
      {children}
    </Context>
  );
}
export const useTheme = () => useContext(Context);
