import { useEffect, useState } from "react"

import { ThemeProviderContext, type Theme } from "@/lib/theme"

const STORAGE_KEY = "flowmon-theme"

function readStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === "dark" || stored === "light" || stored === "system") return stored
  } catch {
    // Storage can be unavailable (private mode, blocked cookies); fall back to system.
  }
  return "system"
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStoredTheme)

  useEffect(() => {
    const root = document.documentElement
    const media = window.matchMedia("(prefers-color-scheme: dark)")

    const apply = () => {
      const resolved = theme === "system" ? (media.matches ? "dark" : "light") : theme
      root.classList.remove("light", "dark")
      root.classList.add(resolved)
    }

    apply()
    if (theme !== "system") return
    media.addEventListener("change", apply)
    return () => media.removeEventListener("change", apply)
  }, [theme])

  const setTheme = (next: Theme) => {
    try {
      localStorage.setItem(STORAGE_KEY, next)
    } catch {
      // Not persisted; the choice still applies for this session.
    }
    setThemeState(next)
  }

  return <ThemeProviderContext.Provider value={{ theme, setTheme }}>{children}</ThemeProviderContext.Provider>
}
