// What the sign-in card says while SpyDB installs its own AWS CLI
// (dynamo.AWSCLISetup, sent as the event dynamo:aws-cli-setup). No React or
// Wails imports, so the unit tests can load it.

export type Setup = { stage: string; done: number; total: number; older: string }

const mb = (bytes: number) => Math.round(bytes / 1_000_000)

export function setupText(s: Setup): string {
  const why = s.older ? `your AWS CLI ${s.older} is too old for this` : 'this Mac has no AWS CLI'
  switch (s.stage) {
    case 'downloading':
      return s.total > 0
        ? `Downloading the AWS CLI (${why}): ${mb(s.done)} of ${mb(s.total)} MB`
        : `Downloading the AWS CLI (${why})…`
    case 'installing':
      return 'Installing the AWS CLI in SpyDB’s folder (one time only)…'
    case 'done':
      return 'AWS CLI installed.'
    default:
      return ''
  }
}

// How far along, 0 to 1, or null when there's no number to show.
export function setupFraction(s: Setup): number | null {
  return s.stage === 'downloading' && s.total > 0 ? Math.min(1, s.done / s.total) : null
}
