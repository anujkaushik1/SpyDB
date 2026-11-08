import { parse, stringify } from 'lossless-json'

// JSON text helpers built on lossless-json, which keeps numbers exactly as
// written: 12345678901234567890 stays that, where JSON.parse would round it.

// Compact JSON text, numbers untouched. Throws if the text isn't valid JSON.
export const normalize = (text: string) => stringify(parse(text)) ?? ''

// Postgres sends jsonb on one line; show it indented, numbers untouched.
// Text that isn't valid JSON comes back as it is.
export function pretty(value: string | null) {
  if (value === null) return ''
  try {
    return stringify(parse(value), null, 2) ?? value
  } catch {
    return value
  }
}
