import { CancelError, Events } from '@wailsio/runtime'
import { useEffect, useRef, useState } from 'react'
import { Service as Updater, type Status } from '../bindings/github.com/anujkaushik1/SpyDB/updater'
import { errorMessage } from './Banner'
import { copyText } from './clipboard'
import { Spinner } from './icons'

// How far Go's Install has got (updater.Progress, sent as an event).
type Progress = { step: 'download' | 'install' | 'restart', percent: number }

// When SpyDB opens, asks GitHub (through Go's updater package) for a newer
// release and offers to install it. An update that updates.json requires
// for this platform can't be put off: Quit instead of Later.
export default function UpdateCheck() {
  const [status, setStatus] = useState<Status | null>(null)

  useEffect(() => {
    let live = true
    const call = Updater.Check()
    call.then((s) => { if (live && s.available) setStatus(s) }, () => {}) // offline: say nothing
    return () => { live = false; call.cancel() }
  }, [])

  return status && <UpdateDialog status={status} onLater={() => setStatus(null)} />
}

function UpdateDialog({ status, onLater }: { status: Status, onLater: () => void }) {
  const [progress, setProgress] = useState<Progress | null>(null) // set while installing
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const running = useRef<ReturnType<typeof Updater.Install> | null>(null)

  useEffect(() => Events.On('spydb:update-progress', (e) => setProgress(e.data as Progress)), [])

  const installing = progress !== null
  const install = async () => {
    setError(null)
    setProgress({ step: 'download', percent: 0 })
    const call = Updater.Install() // on success SpyDB quits and opens again
    running.current = call
    try {
      await call
    } catch (err) {
      if (!(err instanceof CancelError)) setError(errorMessage(err)) // Cancel: back to the question
      setProgress(null)
    } finally {
      running.current = null
    }
  }
  // Only while downloading: stopping the install itself could leave it half done.
  const cancel = () => running.current?.cancel()
  const copy = async () => {
    await copyText(status.command)
    setCopied(true)
  }
  const later = () => { if (!installing && !status.required) onLater() }

  const where = status.platform === 'mac' ? 'Terminal' : 'a terminal'
  return (
    <div className="palette-backdrop centered" onMouseDown={later}>
      <div className="save-query update" role="alertdialog" aria-label="Update SpyDB"
           onMouseDown={(e) => e.stopPropagation()}
           onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); later() } }}>
        <p className="save-query-title">
          {status.required ? `Update SpyDB to ${status.latest}` : `SpyDB ${status.latest} is out`}
        </p>
        <p className="save-query-note">
          You have {status.current}.{' '}
          {status.required
            ? 'This update is required: SpyDB can’t be used until it’s installed.'
            : 'Install it now? SpyDB restarts with it. Your saved connections and queries stay.'}
        </p>

        {!status.canInstall && (
          <>
            <p className="save-query-note">{status.problem} Install it from {where} instead, with:</p>
            <code className="update-command">{status.command}</code>
          </>
        )}
        {installing && (
          <p className="update-progress">
            <Spinner />
            {progress.step === 'download' && `Downloading… ${progress.percent}%`}
            {progress.step === 'install' && (status.platform === 'ubuntu'
              ? 'Installing… Type your password in the window that opens.'
              : 'Installing…')}
            {progress.step === 'restart' && 'Restarting SpyDB…'}
          </p>
        )}
        {error && (
          <>
            <p className="delete-rows-error">{error}</p>
            {status.canInstall && (
              <>
                <p className="save-query-note">Or install it from {where}, with:</p>
                <code className="update-command">{status.command}</code>
              </>
            )}
          </>
        )}

        <div className="save-query-actions">
          {progress?.step === 'download' && <button type="button" className="btn btn-secondary btn-sm" onClick={cancel}>Cancel</button>}
          {status.required
            ? <button type="button" className="btn btn-secondary btn-sm" onClick={() => Updater.Quit()} disabled={installing}>Quit SpyDB</button>
            : <button type="button" className="btn btn-secondary btn-sm" onClick={later} disabled={installing}>Later</button>}
          {(!status.canInstall || error) && (
            <button type="button" className="btn btn-secondary btn-sm" onClick={copy}>{copied ? 'Copied' : 'Copy command'}</button>
          )}
          {status.canInstall && (
            <button type="button" className="btn btn-primary btn-sm" onClick={install} disabled={installing} autoFocus>
              {error ? 'Try again' : 'Install and restart'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
