import { useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { DynamoAwsCliSetup } from '../../bindings/events'
import { Service as Dynamo, type AWSCLISetup, type AWSIdentity, type AWSProfile, type Connection, type SavedKeys } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import Banner, { errorMessage } from '../Banner'
import { CloudIcon, EyeIcon, EyeOffIcon, Spinner } from '../icons'
import { isMac } from '../platform'
import { awsFolder, awsTerminal, setupFraction, setupText } from './setupText'

// What signing in does for each kind of profile (AWSProfile.kind).
const KINDS: Record<string, { label: string; hint: string }> = {
  keys: { label: 'access keys', hint: `Uses the access keys saved for this profile in ${awsFolder()}.` },
  sso: { label: 'AWS SSO', hint: 'Signs in through your browser, if needed.' },
  login: { label: 'browser sign-in', hint: 'Signs in through your browser, if it’s been over 12 hours.' },
  role: { label: 'role', hint: 'Switches into the role this profile names.' },
  process: { label: 'credential program', hint: 'Gets keys from the program this profile names (credential_process).' },
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

// The DynamoDB side of the connect screen: sign in to AWS with one of your
// profiles from ~/.aws (dynamo.Service.AWSSignIn), or with your AWS console
// account through the browser (AWSBrowserSignIn), which needs no setup.
// Either may first install SpyDB's own AWS CLI, with its progress shown.
// Once signed in, it connects to that profile's DynamoDB (Connect) and
// onConnect opens its tables.
// A profile (name) or saved keys (keys: their id) picked in the connections
// sidebar: show it, and sign in if asked.
export type ProfilePick = { name?: string; keys?: string; connect: boolean; seq: number }

type Props = {
  onConnect: (connection: Connection, identity: AWSIdentity) => void
  pick?: ProfilePick | null
  onProfiles?: (profiles: AWSProfile[] | null, error: string | null) => void // for the sidebar's DynamoDB list
  onSavedKeys?: (keys: SavedKeys[]) => void // the keys saved on this computer, for the sidebar too
  onProfileChange?: (name: string) => void // the profile the form shows now
}

export default function DynamoSignIn({ onConnect, pick, onProfiles, onSavedKeys, onProfileChange }: Props) {
  // Sign in with an access key and secret key typed in (KeySignIn), optionally saved on this computer.
  const [keys, setKeys] = useState({ name: '', accessKeyId: '', secretAccessKey: '', region: 'us-east-1', save: true })
  const [showSecret, setShowSecret] = useState(false)
  const [savedKeys, setSavedKeys] = useState<SavedKeys[]>([])
  const [savedPicked, setSavedPicked] = useState<SavedKeys | null>(null) // saved keys picked in the sidebar
  const loadSavedKeys = () => Dynamo.SavedAccessKeys().then((list) => { setSavedKeys(list ?? []); onSavedKeys?.(list ?? []) }).catch(() => {})
  const [profiles, setProfiles] = useState<AWSProfile[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [region, setRegion] = useState('us-east-1')
  const [busy, setBusy] = useState<'profile' | 'browser' | 'keys' | null>(null)
  const [error, setError] = useState<string | null>(null)
  // SpyDB installing its own AWS CLI first (none, or too old): Go reports how it goes.
  const [setup, setSetup] = useState<AWSCLISetup | null>(null)

  // Profiles, and which one to start with (or a just-made browser sign-in).
  const loadProfiles = (select?: string) =>
    Dynamo.AWSProfiles()
      .then((list) => {
        const all = list ?? []
        setProfiles(all)
        onProfiles?.(all, null)
        setName((current) => select ?? (current || (all.find((p) => p.name === 'default') ?? all[0])?.name || ''))
        const home = all.find((p) => p.name === 'default')?.region || all.find((p) => p.region)?.region
        if (home && !select) setRegion(home)
      })
      .catch((err) => { setLoadError(errorMessage(err)); onProfiles?.(null, errorMessage(err)) })

  useEffect(() => { onProfileChange?.(name) }, [name])
  // Picked in the sidebar: show that profile or those saved keys; a double-click signs in.
  useEffect(() => {
    if (!pick || !profiles) return
    setError(null)
    if (pick.keys) {
      const saved = savedKeys.find((k) => k.id === pick.keys)
      setSavedPicked(saved ?? null)
      if (saved) setKeys((k) => ({ ...k, name: saved.name, accessKeyId: '', secretAccessKey: '', region: saved.region }))
      if (pick.connect && busy === null) run('keys', Dynamo.SavedKeySignIn(pick.keys))
      return
    }
    if (pick.name) {
      setName(pick.name)
      if (pick.connect && busy === null) run('profile', Dynamo.AWSSignIn(pick.name))
    }
  }, [pick?.seq, profiles === null])

  useEffect(() => {
    loadProfiles()
    loadSavedKeys()
    const stopListening = Events.On(DynamoAwsCliSetup, (event) => setSetup(event.data))
    return () => {
      stopListening()
      Dynamo.CancelSignIn() // leaving the screen stops a sign-in still waiting
    }
  }, [])

  const run = async (which: 'profile' | 'browser' | 'keys', call: Promise<AWSIdentity>) => {
    setBusy(which)
    setError(null)
    try {
      const identity = await call
      if (which === 'browser') loadProfiles(identity.profile) // it's a profile now
      if (which === 'keys') { loadSavedKeys(); setKeys((k) => ({ ...k, secretAccessKey: '' })) }
      onConnect(await Dynamo.Connect(identity.profile), identity) // this screen gives way to the tables
    } catch (err) {
      setError(errorMessage(err)) // "Sign-in cancelled." after Cancel
    } finally {
      setBusy(null)
      setSetup(null)
    }
  }
  // Go stops the sign-in, which then ends normally with "Sign-in cancelled."
  const cancel = () => Dynamo.CancelSignIn()

  const profile = profiles?.find((p) => p.name === name)
  const kind = KINDS[profile?.kind ?? ''] ?? { label: profile?.kind ?? '', hint: '' }
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
                  onChange={(e) => { setName(e.target.value); setError(null) }}>
            {profiles.map((p) => (
              <option key={p.name} value={p.name}>{p.name} · {KINDS[p.kind]?.label ?? p.kind}</option>
            ))}
          </select>
          <p className="aws-note">
            Region: {profile?.region || 'none set in this profile'}. Profiles come from {awsFolder()}; SpyDB never reads out or stores your keys.
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
      {error && <Banner tone="error" title="Couldn’t sign in" message={error} />}

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
        <section className="aws-keys" aria-label="Sign in with access keys">
          {noProfiles ? null : <p className="aws-or"><span>or</span></p>}
          <p className="aws-keys-title">{savedPicked ? <>Saved keys: <strong>{savedPicked.name}</strong> ({savedPicked.accessKeyId} · {savedPicked.region})</> : 'Access keys'}</p>
          {savedPicked ? (
            <div className="aws-keys-row">
              <span className="hint">Double-click them in the sidebar, or:</span>
              <button type="button" className="btn btn-primary" disabled={busy !== null} onClick={() => run('keys', Dynamo.SavedKeySignIn(savedPicked.id))}>
                {busy === 'keys' ? <><Spinner /> Signing in…</> : 'Sign in with these keys'}
              </button>
              <button type="button" className="link-btn" onClick={() => setSavedPicked(null)}>Type other keys</button>
            </div>
          ) : (
            <form className="aws-keys-form" onSubmit={(e) => { e.preventDefault(); run('keys', Dynamo.KeySignIn(keys)) }}>
              <div className="form-row">
                <div className="field">
                  <label className="label" htmlFor="aws-key-id">Access key ID</label>
                  <input id="aws-key-id" className="input" value={keys.accessKeyId} disabled={busy !== null} autoComplete="off" spellCheck={false}
                         placeholder="AKIA…" onChange={(e) => setKeys({ ...keys, accessKeyId: e.target.value })} />
                </div>
                <div className="field">
                  <label className="label" htmlFor="aws-key-secret">Secret access key</label>
                  <div className="input-with-btn">
                    <input id="aws-key-secret" className="input" type={showSecret ? 'text' : 'password'} value={keys.secretAccessKey} disabled={busy !== null}
                           autoComplete="off" spellCheck={false} onChange={(e) => setKeys({ ...keys, secretAccessKey: e.target.value })} />
                    <button type="button" className="input-btn" aria-label={showSecret ? 'Hide the secret' : 'Show the secret'} onClick={() => setShowSecret((v) => !v)}>
                      {showSecret ? <EyeOffIcon /> : <EyeIcon />}
                    </button>
                  </div>
                </div>
              </div>
              <div className="form-row">
                <div className="field">
                  <label className="label" htmlFor="aws-key-region">Region</label>
                  <select id="aws-key-region" className="select" value={keys.region} disabled={busy !== null} onChange={(e) => setKeys({ ...keys, region: e.target.value })}>
                    {(REGIONS.includes(keys.region) ? REGIONS : [keys.region, ...REGIONS]).map((r) => <option key={r} value={r}>{r}</option>)}
                  </select>
                </div>
                <div className="field">
                  <label className="label" htmlFor="aws-key-name">Save as</label>
                  <input id="aws-key-name" className="input" value={keys.name} disabled={busy !== null || !keys.save} autoComplete="off" spellCheck={false}
                         placeholder={keys.accessKeyId ? `${keys.accessKeyId.slice(0, 4)}… @ ${keys.region}` : 'A name for these keys'} onChange={(e) => setKeys({ ...keys, name: e.target.value })} />
                </div>
              </div>
              <div className="aws-keys-row">
                <label className="checkbox-label">
                  <input type="checkbox" checked={keys.save} disabled={busy !== null} onChange={(e) => setKeys({ ...keys, save: e.target.checked })} />
                  Save on this {isMac ? 'Mac' : 'computer'}
                </label>
                {busy === 'keys' && <button type="button" className="btn btn-secondary" onClick={cancel}>Cancel</button>}
                <button type="submit" className="btn btn-primary" disabled={busy !== null || !keys.accessKeyId.trim() || !keys.secretAccessKey.trim()}>
                  {busy === 'keys' ? <><Spinner /> Signing in…</> : 'Sign in with keys'}
                </button>
              </div>
              <p className="aws-note">
                Keys you save are kept only on this {isMac ? 'Mac' : 'computer'}, in a file readable by your user alone, and go nowhere but to AWS. Nothing is written to {awsFolder()}.
              </p>
            </form>
          )}
        </section>
      )}

      {profiles && (
        <section className="aws-browser" aria-label="Sign in with your browser">
          {noProfiles
            ? <Banner tone="info" title="No AWS profiles yet"
                      message={`Sign in with your AWS account in your browser, type access keys above, or set up a profile in ${awsTerminal()} (aws configure).`} />
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
