/** Subnets as typed: separated by commas, spaces or new lines. */
export function parseSubnets(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

