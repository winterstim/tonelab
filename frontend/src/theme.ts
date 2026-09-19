// Applied to the root as one attribute rather than a swapped stylesheet, so
// a change is immediate. "system" leaves the choice to the media query.
import { useEffect, useState } from "react";

export type Theme = "light" | "dark" | "system";

const query = () => window.matchMedia("(prefers-color-scheme: dark)");

export function applyTheme(name: Theme) {
    const dark = name === "dark" || (name === "system" && query().matches);
    document.documentElement.dataset.theme = dark ? "dark" : "light";
}

// The chosen theme lives with the settings in the backend; this only holds
// what the window is showing right now, which may be unsaved.
export function useTheme(): [Theme, (name: Theme) => void] {
    const [theme, setTheme] = useState<Theme>("system");

    useEffect(() => {
        applyTheme(theme);
        if (theme !== "system") return;
        const media = query();
        const follow = () => applyTheme("system");
        media.addEventListener("change", follow);
        return () => media.removeEventListener("change", follow);
    }, [theme]);

    return [theme, setTheme];
}

// Shared between the settings screen, which chooses, and the root, which
// applies; nothing else needs it.
import { createContext, useContext } from "react";
export const ThemeContext = createContext<[Theme, (name: Theme) => void]>(["system", () => {}]);
export const useThemeChoice = () => useContext(ThemeContext);
