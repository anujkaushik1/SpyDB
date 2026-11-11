import { useEffect, useRef, useState } from 'react'
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
  role: { label: 'role', hint: 'Switches into the role this profile names.' },
  other: { label: 'other', hint: 'Signs in the way the AWS CLI does for this profile.' },
}

type Result = { identity: AWSIdentity } | { error: string }

// The DynamoDB side of the connect screen: pick one of your AWS profiles (from
// ~/.aws) and sign in with it (dynamo.Service.AWSSignIn). Listing tables comes next.
export default function DynamoSignIn() {
  const [profiles, setProfiles] = useState<AWSProfile[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<Result | null>(null)
  // SpyDB installing its own AWS CLI first (none, or too old): Go reports how it goes.
  const [setup, setSetup] = useState<AWSCLISetup | null>(null)
  const pending = useRef<{ cancel: () => void } | null>(null)
  const cancelled = useRef(false)

  useEffect(() => {
    Dynamo.AWSProfiles()
      .then((list) => {
        const all = list ?? []
        setProfiles(all)
        setName((all.find((p) => p.name === 'default') ?? all[0])?.name ?? '')
      })
      .catch((err) => setLoadError(errorMessage(err)))
    const stopListening = Events.On(DynamoAwsCliSetup, (event) => setSetup(event.data))
    return () => {
      stopListening()
      pending.current?.cancel() // leaving the screen stops a sign-in still waiting
    }
  }, [])

  const signIn = async () => {
    setBusy(true)
    setResult(null)
    cancelled.current = false
    const call = Dynamo.AWSSignIn(name)
    pending.current = call
    try {
      setResult({ identity: await call })
    } catch (err) {
      setResult({ error: cancelled.current ? 'Sign-in cancelled.' : errorMessage(err) })
    } finally {
      pending.current = null
      setBusy(false)
      setSetup(null)
    }
  }
  const cancel = () => {
    cancelled.current = true
    pending.current?.cancel()
  }

  const profile = profiles?.find((p) => p.name === name)
  const kind = KINDS[profile?.kind ?? 'other'] ?? KINDS.other

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
      {profiles?.length === 0 && (
        <Banner tone="info" title="No AWS profiles yet"
                message="Set one up in Terminal, then come back: aws configure (access keys) or aws configure sso (your company’s AWS sign-in)." />
      )}

      {profiles && profiles.length > 0 && (
        <>
          <div className="field">
            <label className="label" htmlFor="aws-profile">AWS profile</label>
            <select id="aws-profile" className="select" value={name} disabled={busy}
                    onChange={(e) => { setName(e.target.value); setResult(null) }}>
              {profiles.map((p) => (
                <option key={p.name} value={p.name}>{p.name} · {KINDS[p.kind]?.label ?? p.kind}</option>
              ))}
            </select>
            <p className="aws-note">
              Region: {profile?.region || 'none set in this profile'}. Profiles come from ~/.aws; SpyDB never reads out or stores your keys.
            </p>
          </div>

          {busy && setup && setup.stage !== 'done' && (
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

          <footer className="card-footer">
            <span className="hint">{busy && setup && setup.stage !== 'done' ? 'Getting the AWS CLI first…' : busy && profile?.kind === 'sso' ? 'Finish signing in in your browser…' : kind.hint}</span>
            <div className="actions">
              {busy && <button type="button" className="btn btn-secondary" onClick={cancel}>Cancel</button>}
              <button type="button" className="btn btn-primary" onClick={signIn} disabled={busy || !name}>
                {busy ? <><Spinner /> Signing in…</> : 'Sign in to AWS'}
              </button>
            </div>
          </footer>
        </>
      )}
    </div>
  )
}
