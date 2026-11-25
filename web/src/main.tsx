import { QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter, Navigate, Route, Routes } from "react-router"

import "./index.css"
import { AppLayout } from "@/components/app-layout"
import { AuthGate } from "@/components/auth-gate"
import { ThemeProvider } from "@/components/theme-provider"
import { TooltipProvider } from "@/components/ui/tooltip"
import { ApiError } from "@/lib/api"
import { navItems } from "@/lib/navigation"
import { ExportersPage } from "@/pages/exporters-page"
import { FlowExplorerPage } from "@/pages/flow-explorer-page"
import { DevicePage, LocalHostPage, LocalHostsPage } from "@/pages/host-pages"
import { MikroTikGuidePage } from "@/pages/mikrotik-guide-page"
import { ClassificationGuidePage } from "@/pages/classification-guide-page"
import { OverviewPage } from "@/pages/overview-page"
import { NotFoundPage, PlaceholderPage } from "@/pages/placeholder-page"
import { RemoteIPPage, RemoteIPsPage } from "@/pages/remote-pages"
import { DNSDomainPage, DNSPage } from "@/pages/dns-pages"
import { SNMPDevicePage } from "@/pages/snmp-pages"
import { DatabasesSettings, GeoIPCard, SettingsLayout, SourcesSettings } from "@/pages/settings-page"
import { InternetLineCard, LocalNetworksCard, RetentionCard } from "@/components/collection-settings"
import { ServiceNamesCard } from "@/components/service-names-card"

/** Screens that exist; every other navigation item shows a placeholder. */
const screens: Record<string, React.ReactNode> = {
  "/": <OverviewPage />,
  "/exporters": <ExportersPage />,
  "/hosts": <LocalHostsPage />,
  "/flows": <FlowExplorerPage />,
  "/remote": <RemoteIPsPage />,
  "/dns": <DNSPage />,
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
                  {navItems
                    .filter((item) => item.path !== "/settings")
                    .map((item) => (
                      <Route
                        key={item.path}
                        path={item.path}
                        element={screens[item.path] ?? <PlaceholderPage item={item} />}
                      />
                    ))}
                  <Route path="/settings" element={<SettingsLayout />}>
                    <Route index element={<Navigate to="local-networks" replace />} />
                    <Route path="local-networks" element={<LocalNetworksCard />} />
                    <Route path="internet-line" element={<InternetLineCard />} />
                    <Route path="service-names" element={<ServiceNamesCard />} />
                    <Route path="collector" element={<Navigate to="/exporters" replace />} />
                    <Route path="sources/:kind" element={<SourcesSettings />} />
                    <Route path="retention" element={<RetentionCard />} />
                    <Route path="databases" element={<DatabasesSettings />} />
                    <Route path="geoip" element={<GeoIPCard />} />
                  </Route>
                  <Route path="/hosts/*" element={<LocalHostPage />} />
                  <Route path="/devices/:mac" element={<DevicePage />} />
                  <Route path="/remote/:ip" element={<RemoteIPPage />} />
                  <Route path="/dns/:domain" element={<DNSDomainPage />} />
                  <Route path="/snmp/:id" element={<SNMPDevicePage />} />
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
