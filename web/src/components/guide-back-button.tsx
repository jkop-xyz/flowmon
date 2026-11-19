import { ArrowLeft } from "lucide-react"
import { useLocation, useNavigate } from "react-router"

import { Button } from "@/components/ui/button"

/** Back to where the guide was opened from, or to fallback when it was opened first. */
export function GuideBackButton({ fallback, fallbackLabel }: { fallback: string; fallbackLabel: string }) {
  const navigate = useNavigate()
  // "default" is the first page of the session (a bookmark or a reload): nothing to go back to.
  const { key } = useLocation()
  return (
    <Button
      variant="ghost"
      size="sm"
      className="-ml-2 self-start"
      onClick={() => (key === "default" ? navigate(fallback) : navigate(-1))}
    >
      <ArrowLeft />
      {key === "default" ? fallbackLabel : "Back"}
    </Button>
  )
}
