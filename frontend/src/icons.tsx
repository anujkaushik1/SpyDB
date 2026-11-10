import type { ReactNode, SVGProps } from 'react'

// Stroke icons on a 24px grid (Lucide shapes). Size them with CSS.
function Icon({ children, ...props }: SVGProps<SVGSVGElement> & { children: ReactNode }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}
         strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" {...props}>
      {children}
    </svg>
  )
}

export const DatabaseIcon = () => (
  <Icon>
    <ellipse cx="12" cy="5" rx="9" ry="3"/>
    <path d="M3 5V19A9 3 0 0 0 21 19V5"/>
    <path d="M3 12A9 3 0 0 0 21 12"/>
  </Icon>
)

export const CheckIcon = () => (
  <Icon>
    <circle cx="12" cy="12" r="10"/>
    <path d="m9 12 2 2 4-4"/>
  </Icon>
)

export const AlertIcon = () => (
  <Icon>
    <circle cx="12" cy="12" r="10"/>
    <line x1="12" x2="12" y1="8" y2="12"/>
    <line x1="12" x2="12.01" y1="16" y2="16"/>
  </Icon>
)

export const InfoIcon = () => (
  <Icon>
    <circle cx="12" cy="12" r="10"/>
    <line x1="12" x2="12" y1="16" y2="12"/>
    <line x1="12" x2="12.01" y1="8" y2="8"/>
  </Icon>
)

export const EyeIcon = () => (
  <Icon>
    <path d="M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0"/>
    <circle cx="12" cy="12" r="3"/>
  </Icon>
)

export const EyeOffIcon = () => (
  <Icon>
    <path d="M10.733 5.076a10.744 10.744 0 0 1 11.205 6.575 1 1 0 0 1 0 .696 10.747 10.747 0 0 1-1.444 2.49"/>
    <path d="M14.084 14.158a3 3 0 0 1-4.242-4.242"/>
    <path d="M17.479 17.499a10.75 10.75 0 0 1-15.417-5.151 1 1 0 0 1 0-.696 10.75 10.75 0 0 1 4.446-5.143"/>
    <path d="m2 2 20 20"/>
  </Icon>
)

export const TableIcon = () => (
  <Icon>
    <path d="M12 3v18"/>
    <rect width="18" height="18" x="3" y="3" rx="2"/>
    <path d="M3 9h18"/>
    <path d="M3 15h18"/>
  </Icon>
)

export const LayersIcon = () => (
  <Icon>
    <path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"/>
    <path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"/>
    <path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"/>
  </Icon>
)

export const SearchIcon = () => (
  <Icon>
    <circle cx="11" cy="11" r="8"/>
    <path d="m21 21-4.3-4.3"/>
  </Icon>
)

export const ChevronIcon = () => (
  <Icon>
    <path d="m9 18 6-6-6-6"/>
  </Icon>
)

export const KeyIcon = () => (
  <Icon>
    <path d="M2.586 17.414A2 2 0 0 0 2 18.828V21a1 1 0 0 0 1 1h3a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h1a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h.172a2 2 0 0 0 1.414-.586l.814-.814a6.5 6.5 0 1 0-4-4z"/>
    <circle cx="16.5" cy="7.5" r=".5" fill="currentColor"/>
  </Icon>
)

// A foreign key column (chain link).
export const LinkIcon = () => (
  <Icon>
    <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
    <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
  </Icon>
)

// Go to the row a foreign key value points at.
export const ArrowUpRightIcon = () => (
  <Icon>
    <path d="M7 7h10v10"/>
    <path d="M7 17 17 7"/>
  </Icon>
)

export const CodeIcon = () => (
  <Icon>
    <polyline points="16 18 22 12 16 6"/>
    <polyline points="8 6 2 12 8 18"/>
  </Icon>
)

export const PowerIcon = () => (
  <Icon>
    <path d="M12 2v10"/>
    <path d="M18.4 6.6a9 9 0 1 1-12.77.04"/>
  </Icon>
)

export const ActivityIcon = () => (
  <Icon>
    <path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2"/>
  </Icon>
)

export const XIcon = () => (
  <Icon>
    <path d="M18 6 6 18"/>
    <path d="m6 6 12 12"/>
  </Icon>
)

export const PlusIcon = () => (
  <Icon>
    <path d="M5 12h14"/>
    <path d="M12 5v14"/>
  </Icon>
)

export const ArrowUpIcon = () => (
  <Icon>
    <path d="m5 12 7-7 7 7"/>
    <path d="M12 19V5"/>
  </Icon>
)

export const ArrowDownIcon = () => (
  <Icon>
    <path d="M12 5v14"/>
    <path d="m19 12-7 7-7-7"/>
  </Icon>
)

export const ChevronLeftIcon = () => (
  <Icon>
    <path d="m15 18-6-6 6-6"/>
  </Icon>
)

export const ChevronDownIcon = () => (
  <Icon>
    <path d="m6 9 6 6 6-6"/>
  </Icon>
)

export const TickIcon = () => (
  <Icon>
    <path d="M20 6 9 17l-5-5"/>
  </Icon>
)

export const TrashIcon = () => (
  <Icon>
    <path d="M3 6h18"/>
    <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/>
    <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
  </Icon>
)

export const DownloadIcon = () => (
  <Icon>
    <path d="M12 15V3"/>
    <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
    <path d="m7 10 5 5 5-5"/>
  </Icon>
)

export const UploadIcon = () => (
  <Icon>
    <path d="M12 3v12"/>
    <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
    <path d="m17 8-5-5-5 5"/>
  </Icon>
)

export const PlayIcon = () => (
  <Icon>
    <polygon points="6 3 20 12 6 21 6 3"/>
  </Icon>
)

export const RefreshIcon = () => (
  <Icon>
    <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/>
    <path d="M21 3v5h-5"/>
  </Icon>
)

export const Spinner = () => (
  <Icon className="spin">
    <path d="M21 12a9 9 0 1 1-6.219-8.56"/>
  </Icon>
)

export const PencilIcon = () => (
  <Icon>
    <path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/>
    <path d="m15 5 4 4"/>
  </Icon>
)
