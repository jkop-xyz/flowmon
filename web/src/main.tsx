import { QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter, Route, Routes } from "react-router"

import "./index.css"
import { AppLayout } from "@/components/app-layout"
import { AuthGate } from "@/components/auth-gate"
import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import { ApiError } from "@/lib/api"
import { navItems } from "@/lib/navigation"
import { ExportersPage } from "@/pages/exporters-page"
import { FlowExplorerPage } from "@/pages/flow-explorer-page"
import { LocalHostPage, LocalHostsPage } from "@/pages/host-pages"
import { MikroTikGuidePage } from "@/pages/mikrotik-guide-page"
import { ClassificationGuidePage } from "@/pages/classification-guide-page"
import { OverviewPage } from "@/pages/overview-page"
import { NotFoundPage, PlaceholderPage } from "@/pages/placeholder-page"
import { RemoteIPPage, RemoteIPsPage } from "@/pages/remote-pages"
import { DNSDomainPage, DNSPage } from "@/pages/dns-pages"
import { SettingsPage } from "@/pages/settings-page"

/** Screens that exist; every other navigation item shows a placeholder. */
const screens: Record<string, React.ReactNode> = {
  "/": <OverviewPage />,
  "/exporters": <ExportersPage />,
  "/hosts": <LocalHostsPage />,
  "/flows": <FlowExplorerPage />,
  "/remote": <RemoteIPsPage />,
  "/dns": <DNSPage />,
  "/settings": <SettingsPage />,
}

const queryClient: QueryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 10_000, retry: 1 } },
  // A 401 from any query means the session ended: re-check it, which shows the login page.
  queryCache: new QueryCache({
    onError: (error) => {
      if (error instanceof ApiError && error.status === 401) {
        void queryClient.invalidateQueries({ queryKey: ["session"] })
        void queryClient.invalidateQueries({ queryKey: ["setup"] })
      }
    },
  }),
})

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <TooltipProvider>
          <AuthGate>
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
                  <Route path="/hosts/*" element={<LocalHostPage />} />
                  <Route path="/remote/:ip" element={<RemoteIPPage />} />
                  <Route path="/dns/:domain" element={<DNSDomainPage />} />
                  <Route path="/guide/mikrotik" element={<MikroTikGuidePage />} />
                  <Route path="/guide/classification" element={<ClassificationGuidePage />} />
                  <Route path="*" element={<NotFoundPage />} />
                </Route>
              </Routes>
            </BrowserRouter>
          </AuthGate>
        </TooltipProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
)
