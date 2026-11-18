import { useQuery } from "@tanstack/react-query"
import { AlertTriangle } from "lucide-react"
import type { ReactNode } from "react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Skeleton } from "@/components/ui/skeleton"
import { fetchSession, fetchSetupStatus } from "@/lib/auth"
import { LoginPage, SetupPage } from "@/pages/auth-pages"

/** Shows first-run setup, then the login page, and the app once logged in. */
export function AuthGate({ children }: { children: ReactNode }) {
  const setup = useQuery({ queryKey: ["setup"], queryFn: ({ signal }) => fetchSetupStatus(signal), retry: false })
  const done = setup.data?.step === "done"
  const session = useQuery({
    queryKey: ["session"],
    queryFn: ({ signal }) => fetchSession(signal),
    enabled: done,
    retry: false,
    refetchInterval: 60_000,
  })

  if (setup.isPending || (done && session.isPending)) {
    return (
      <div className="flex min-h-svh items-center justify-center">
        <Skeleton className="h-48 w-full max-w-sm" />
      </div>
    )
  }
  if (setup.isError || session.isError) {
    return (
      <div className="flex min-h-svh items-center justify-center p-4">
        <Alert variant="destructive" className="max-w-md">
          <AlertTriangle />
          <AlertTitle>The App Store isn't available</AlertTitle>
          <AlertDescription>
            Flowmon is still collecting Flows, but accounts and settings can't be read right now. It retries on its own;
            reload this page in a moment.
          </AlertDescription>
        </Alert>
      </div>
    )
  }
  if (!done) return <SetupPage status={setup.data} />
  if (!session.data) return <LoginPage />
  return children
}
