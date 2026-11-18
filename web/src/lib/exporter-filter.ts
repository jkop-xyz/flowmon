import { useSearchParams } from "react-router"

/**
 * The global Exporter filter, kept in the URL (?exporter=192.168.10.1) like the time
 * range. Empty means all Exporters.
 */
export function useExporterFilter() {
  const [params, setParams] = useSearchParams()
  const exporter = params.get("exporter") ?? ""
  const setExporter = (next: string) => {
    setParams(
      (prev) => {
        const updated = new URLSearchParams(prev)
        if (next) updated.set("exporter", next)
        else updated.delete("exporter")
        return updated
      },
      { replace: true },
    )
  }
  return { exporter, setExporter }
}
