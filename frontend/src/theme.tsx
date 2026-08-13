import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import type { Theme } from "./types";
import { get, put, toErrorMessage } from "./api";
import { decodeGeneral } from "./types";
import { useAuth } from "./auth";

type ThemeContextValue = {
  theme: Theme;
  applyTheme: (theme: Theme) => void;
  saveTheme: (theme: Theme) => Promise<void>;
  error: string;
};
const ThemeContext = createContext<ThemeContextValue | null>(null);
const validTheme = (value: string | null): value is Theme =>
  value === "system" || value === "dark" || value === "light";

export function ThemeProvider({ children }: { children: ReactNode }) {
  const { authenticated } = useAuth();
  const stored = localStorage.getItem("theme");
  const [theme, setThemeState] = useState<Theme>(
    validTheme(stored) ? stored : "system",
  );
  const [error, setError] = useState("");
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme:dark)");
    const apply = () => {
      document.documentElement.dataset.theme =
        theme === "system" ? (media.matches ? "dark" : "light") : theme;
    };
    apply();
    if (theme === "system") media.addEventListener("change", apply);
    localStorage.setItem("theme", theme);
    return () => media.removeEventListener("change", apply);
  }, [theme]);
  useEffect(() => {
    if (!authenticated) return;
    const controller = new AbortController();
    void get("settings/general", decodeGeneral, controller.signal)
      .then((settings) => setThemeState(settings.theme))
      .catch((cause) => {
        if (!controller.signal.aborted) setError(toErrorMessage(cause));
      });
    return () => controller.abort();
  }, [authenticated]);
  const saveTheme = async (next: Theme) => {
    const previous = theme;
    setThemeState(next);
    setError("");
    try {
      const general = await get("settings/general", decodeGeneral);
      await put("settings/general", decodeGeneral, { ...general, theme: next });
    } catch (cause) {
      setThemeState(previous);
      setError(toErrorMessage(cause));
      throw cause;
    }
  };
  return (
    <ThemeContext.Provider
      value={{ theme, applyTheme: setThemeState, saveTheme, error }}
    >
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  const value = useContext(ThemeContext);
  if (!value) throw new Error("useTheme 必须在 ThemeProvider 内使用");
  return value;
}
