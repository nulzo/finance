// Minimal CSV export. Handles commas, quotes, newlines and `null`
// without pulling in a dependency.
export function toCSV<T extends Record<string, unknown>>(
  rows: T[],
  columns?: (keyof T)[],
): string {
  if (rows.length === 0) return ""
  const cols = (columns ?? (Object.keys(rows[0]) as (keyof T)[])) as string[]
  const escape = (v: unknown): string => {
    if (v == null) return ""
    const s = typeof v === "object" ? JSON.stringify(v) : String(v)
    if (/[",\n\r]/.test(s)) return `"${s.replace(/"/g, '""')}"`
    return s
  }
  const header = cols.join(",")
  const body = rows
    .map((r) => cols.map((c) => escape((r as Record<string, unknown>)[c])).join(","))
    .join("\n")
  return `${header}\n${body}`
}

export function downloadCSV(filename: string, csv: string): void {
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8;" })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.style.display = "none"
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

export function downloadJSON(filename: string, data: unknown): void {
  const blob = new Blob([JSON.stringify(data, null, 2)], {
    type: "application/json;charset=utf-8;",
  })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.style.display = "none"
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}
