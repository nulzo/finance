// Formatting helpers shared by tables, cards and charts.
//
// Monetary values arrive from the API in two shapes:
//   - `*_cents` fields as integer cents (`number`).
//   - decimal.Decimal as a JSON string ("1234.56").
//
// These helpers accept either and never throw on malformed input —
// that's a table cell, not a business rule.

const USD = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 2,
})

const USD_COMPACT = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  notation: "compact",
  maximumFractionDigits: 2,
})

const NUM = new Intl.NumberFormat("en-US")
const PERCENT = new Intl.NumberFormat("en-US", {
  style: "percent",
  maximumFractionDigits: 2,
})

export function cents(v: number | null | undefined): string {
  if (v == null || Number.isNaN(v)) return "-"
  return USD.format(v / 100)
}

/** Signed USD from integer cents. Positive values are prefixed with
 *  "+" so gains read unambiguously next to losses (USD.format only
 *  prefixes negatives). */
export function signedCents(v: number | null | undefined): string {
  if (v == null || Number.isNaN(v)) return "-"
  const s = USD.format(v / 100)
  return v > 0 ? `+${s}` : s
}

/** Signed percentage from a decimal fraction (e.g. 0.1234 → "+12.34%"). */
export function signedPercent(v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return "-"
  const s = PERCENT.format(v)
  return v > 0 ? `+${s}` : s
}

export function centsCompact(v: number | null | undefined): string {
  if (v == null || Number.isNaN(v)) return "-"
  return USD_COMPACT.format(v / 100)
}

export function dollars(v: number | string | null | undefined): string {
  if (v == null) return "-"
  const n = typeof v === "string" ? Number(v) : v
  if (!Number.isFinite(n)) return "-"
  return USD.format(n)
}

/** Generic number with thousands-separators. */
export function num(v: number | string | null | undefined, digits = 0): string {
  if (v == null) return "-"
  const n = typeof v === "string" ? Number(v) : v
  if (!Number.isFinite(n)) return "-"
  return new Intl.NumberFormat("en-US", {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(n)
}

export function integer(v: number | null | undefined): string {
  if (v == null) return "-"
  return NUM.format(v)
}

export function percent(v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return "-"
  return PERCENT.format(v)
}

export function dateTime(v: string | null | undefined): string {
  if (!v) return "-"
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return v
  return d.toLocaleString()
}

export function dateOnly(v: string | null | undefined): string {
  if (!v) return "-"
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return v
  return d.toLocaleDateString()
}

export function relative(v: string | null | undefined): string {
  if (!v) return "-"
  const d = new Date(v).getTime()
  if (Number.isNaN(d)) return v
  const diff = d - Date.now()
  const abs = Math.abs(diff)
  const min = 60_000,
    hr = 3_600_000,
    day = 86_400_000
  const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" })
  if (abs < min) return rtf.format(Math.round(diff / 1000), "second")
  if (abs < hr) return rtf.format(Math.round(diff / min), "minute")
  if (abs < day) return rtf.format(Math.round(diff / hr), "hour")
  return rtf.format(Math.round(diff / day), "day")
}

export function truncate(v: string | null | undefined, n = 80): string {
  if (!v) return ""
  if (v.length <= n) return v
  return v.slice(0, n - 1) + "…"
}
