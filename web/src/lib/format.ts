const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })

/** "12s ago", "5 min ago", "yesterday"... falls back to a date after a week. */
export function timeAgo(iso: string): string {
  const secs = Math.round((new Date(iso).getTime() - Date.now()) / 1000)
  const abs = Math.abs(secs)
  if (abs < 60) return rtf.format(secs, 'second')
  if (abs < 3600) return rtf.format(Math.round(secs / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(secs / 3600), 'hour')
  if (abs < 7 * 86400) return rtf.format(Math.round(secs / 86400), 'day')
  return new Date(iso).toLocaleDateString()
}

export function dateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

export const providerLabel = (p: string) =>
  ({ generic: 'Generic (HMAC)', razorpay: 'Razorpay', stripe: 'Stripe', shopify: 'Shopify', github: 'GitHub', standardwebhooks: 'Standard Webhooks (Svix)', cashfree: 'Cashfree', payu: 'PayU', phonepe: 'PhonePe' })[p] ?? p

/** 1,284 -> "1.3K"; small numbers unchanged. */
export function compact(n: number): string {
  return Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(n)
}

/** Human label for a contract finding kind. */
const kindLabels: Record<string, string> = {
  missing_field: 'Field missing',
  type_changed: 'Type changed',
  type_widened: 'Integer became decimal',
  null_value: 'Null value',
  new_enum_value: 'New value',
  new_field: 'New field',
}

export const kindLabel = (k: string) => kindLabels[k] ?? k
