import { AlertIcon, CheckIcon, InfoIcon } from './icons'

export type Notice = { tone: 'ok' | 'error' | 'info'; title: string; message: string; detail?: string }

export const errorMessage = (err: unknown) => (err instanceof Error ? err.message : String(err))

// Go's ConnectionError arrives as err.cause = { summary, detail }.
export const errorDetail = (err: unknown): string | undefined => {
  const cause = (err as { cause?: { detail?: unknown } } | null)?.cause
  return typeof cause?.detail === 'string' ? cause.detail : undefined
}

// A result message; `detail` (raw pgx error) is collapsed under "Details".
export default function Banner({ tone, title, message, detail }: Notice) {
  const Icon = tone === 'ok' ? CheckIcon : tone === 'error' ? AlertIcon : InfoIcon
  return (
    <div className={`banner banner-${tone}`}>
      <Icon />
      <div>
        <p className="banner-title">{title}</p>
        <p className="banner-body">{message}</p>
        {detail && (
          <details className="banner-details">
            <summary>Details</summary>
            <pre className="banner-mono">{detail}</pre>
          </details>
        )}
      </div>
    </div>
  )
}
