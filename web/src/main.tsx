import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter, Route, Routes } from "react-router"

import "./index.css"
import { AppLayout } from "@/components/app-layout"
import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import { navItems } from "@/lib/navigation"
import { ExportersPage } from "@/pages/exporters-page"
import { NotFoundPage, PlaceholderPage } from "@/pages/placeholder-page"

/** Screens that exist; every other navigation item shows a placeholder. */
const screens: Record<string, React.ReactNode> = {
  "/exporters": <ExportersPage />,
}

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 10_000, retry: 1 } },
})

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <TooltipProvider>
          <BrowserRouter>
            <Routes>
              <Route element={<AppLayout />}>
                {navItems.map((item) => (
                  <Route
                    key={item.path}
                    path={item.path}
                    element={screens[item.path] ?? <PlaceholderPage item={item} />}
                  />
                ))}
                <Route path="*" element={<NotFoundPage />} />
              </Route>
            </Routes>
          </BrowserRouter>
        </TooltipProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
)
