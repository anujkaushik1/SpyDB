import { useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { DynamoAwsCliSetup } from '../../bindings/events'
import { Service as Dynamo, type AWSCLISetup, type AWSIdentity, type AWSProfile } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import Banner, { errorMessage } from '../Banner'
import { CloudIcon, Spinner } from '../icons'
import { setupFraction, setupText } from './setupText'

// What signing in does for each kind of profile (AWSProfile.kind).
const KINDS: Record<string, { label: string; hint: string }> = {
  keys: { label: 'access keys', hint: 'Uses the access keys saved for this profile in ~/.aws.' },
  sso: { label: 'AWS SSO', hint: 'Signs in through your browser, if needed.' },
  login: { label: 'browser sign-in', hint: 'Signs in through your browser, if it’s been over 12 hours.' },
  role: { label: 'role', hint: 'Switches into the role this profile names.' },
  other: { label: 'no credentials', hint: 'This profile has no keys or sign-in: sign in with your browser below.' },
}

// The profile a browser sign-in is saved as (aws login --profile spydb).
const BROWSER_PROFILE = 'spydb'

// AWS regions to sign in to; the profile's own is added if it isn't here.
const REGIONS = [
  'us-east-1', 'us-east-2', 'us-west-1', 'us-west-2', 'ca-central-1', 'sa-east-1',
  'eu-west-1', 'eu-west-2', 'eu-west-3', 'eu-central-1', 'eu-north-1', 'eu-south-1',
  'ap-south-1', 'ap-south-2', 'ap-southeast-1', 'ap-southeast-2', 'ap-northeast-1', 'ap-northeast-2', 'ap-northeast-3',
  'me-south-1', 'af-south-1',
]

type Result = { identity: AWSIdentity } | { error: string }

// The DynamoDB side of the connect screen: sign in to AWS with one of your
// profiles from ~/.aws (dynamo.Service.AWSSignIn), or with your AWS console
// account through the browser (AWSBrowserSignIn), which needs no setup.
// Either may first install SpyDB's own AWS CLI, with its progress shown.
// Listing tables comes next.
export default function DynamoSignIn() {
  const [profiles, setProfiles] = useState<AWSProfile[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [region, setRegion] = useState('us-east-1')
  const [busy, setBusy] = useState<'profile' | 'browser' | null>(null)
  const [result, setResult] = useState<Result | null>(null)
  // SpyDB installing its own AWS CLI first (none, or too old): Go reports how it goes.
  const [setup, setSetup] = useState<AWSCLISetup | null>(null)

  // Profiles, and which one to start with (or a just-made browser sign-in).
  const loadProfiles = (select?: string) =>
    Dynamo.AWSProfiles()
      .then((list) => {
        const all = list ?? []
        setProfiles(all)
        setName((current) => select ?? (current || (all.find((p) => p.name === 'default') ?? all[0])?.name || ''))
        const home = all.find((p) => p.name === 'default')?.region || all.find((p) => p.region)?.region
        if (home && !select) setRegion(home)
      })
      .catch((err) => setLoadError(errorMessage(err)))

  useEffect(() => {
    loadProfiles()
    const stopListening = Events.On(DynamoAwsCliSetup, (event) => setSetup(event.data))
    return () => {
      stopListening()
      Dynamo.CancelSignIn() // leaving the screen stops a sign-in still waiting
    }
  }, [])

  const run = async (which: 'profile' | 'browser', call: Promise<AWSIdentity>) => {
    setBusy(which)
    setResult(null)
    try {
      const identity = await call
      setResult({ identity })
      if (which === 'browser') loadProfiles(identity.profile) // it's a profile now
    } catch (err) {
      setResult({ error: errorMessage(err) }) // "Sign-in cancelled." after Cancel
    } finally {
      setBusy(null)
      setSetup(null)
    }
  }
  // Go stops the sign-in, which then ends normally with "Sign-in cancelled."
  const cancel = () => Dynamo.CancelSignIn()

  const profile = profiles?.find((p) => p.name === name)
  const kind = KINDS[profile?.kind ?? 'other'] ?? KINDS.other
  const installing = busy !== null && setup !== null && setup.stage !== 'done'
  const regions = REGIONS.includes(region) ? REGIONS : [region, ...REGIONS]
  const noProfiles = profiles?.length === 0

  return (
    <div className="card aws-card">
      <header className="card-header">
        <span className="db-badge db-badge-aws"><CloudIcon /></span>
        <div className="card-heading">
          <h1 className="card-title">Sign in to AWS</h1>
          <p className="card-subtitle">DynamoDB</p>
        </div>
      </header>

      {loadError && <Banner tone="error" title="Couldn’t read your AWS profiles" message={loadError} />}
      {profiles === null && !loadError && <p className="aws-note">Reading your AWS profiles…</p>}

      {profiles && profiles.length > 0 && (
        <div className="field">
          <label className="label" htmlFor="aws-profile">AWS profile</label>
          <select id="aws-profile" className="select" value={name} disabled={busy !== null}
                  onChange={(e) => { setName(e.target.value); setResult(null) }}>
            {profiles.map((p) => (
              <option key={p.name} value={p.name}>{p.name} · {KINDS[p.kind]?.label ?? p.kind}</option>
            ))}
          </select>
          <p className="aws-note">
            Region: {profile?.region || 'none set in this profile'}. Profiles come from ~/.aws; SpyDB never reads out or stores your keys.
          </p>
        </div>
      )}

      {installing && setup && (
        <div className="aws-setup" role="status">
          <p>{setupText(setup)}</p>
          <div className="aws-setup-bar">
            <span style={{ width: `${(setupFraction(setup) ?? 0) * 100}%` }}
                  className={setupFraction(setup) === null ? 'busy' : undefined} />
          </div>
        </div>
      )}
      {result && 'identity' in result && (
        <Banner tone="ok" title="Signed in" message={`${result.identity.arn} · account ${result.identity.account}`} />
      )}
      {result && 'error' in result && <Banner tone="error" title="Couldn’t sign in" message={result.error} />}

      {profiles && profiles.length > 0 && (
        <footer className="card-footer">
          <span className="hint">
            {busy === 'profile' && installing ? 'Getting the AWS CLI first…'
              : busy === 'profile' && (profile?.kind === 'sso' || profile?.kind === 'login') ? 'Finish signing in in your browser…'
              : kind.hint}
          </span>
          <div className="actions">
            {busy === 'profile' && <button type="button" className="btn btn-secondary" onClick={cancel}>Cancel</button>}
            <button type="button" className="btn btn-primary" onClick={() => run('profile', Dynamo.AWSSignIn(name))}
                    disabled={busy !== null || !name}>
              {busy === 'profile' ? <><Spinner /> Signing in…</> : 'Sign in to AWS'}
            </button>
          </div>
        </footer>
      )}

      {profiles && (
        <section className="aws-browser" aria-label="Sign in with your browser">
          {noProfiles
            ? <Banner tone="info" title="No AWS profiles yet"
                      message="Sign in with your AWS account in your browser, or set up a profile in Terminal (aws configure)." />
            : <p className="aws-or"><span>or</span></p>}
          <div className="aws-browser-row">
            <label className="label" htmlFor="aws-region">Region</label>
            <select id="aws-region" className="select aws-region" value={region} disabled={busy !== null}
                    onChange={(e) => setRegion(e.target.value)}>
              {regions.map((r) => <option key={r} value={r}>{r}</option>)}
            </select>
            {busy === 'browser' && <button type="button" className="btn btn-secondary" onClick={cancel}>Cancel</button>}
            <button type="button" className={noProfiles ? 'btn btn-primary' : 'btn btn-secondary'} disabled={busy !== null}
                    onClick={() => run('browser', Dynamo.AWSBrowserSignIn(BROWSER_PROFILE, region))}>
              {busy === 'browser' ? <><Spinner /> {installing ? 'Getting the AWS CLI…' : 'Waiting for your browser…'}</> : 'Sign in with your browser'}
            </button>
          </div>
          <p className="aws-note">
            Uses your AWS console account (the one for aws.amazon.com). SpyDB saves the sign-in as the profile “{BROWSER_PROFILE}”; it lasts up to 12 hours.
          </p>
        </section>
      )}
    </div>
  )
}
