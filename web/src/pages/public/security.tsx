import {
  ActivityIcon,
  DatabaseBackupIcon,
  EyeOffIcon,
  FileClockIcon,
  GaugeIcon,
  KeyRoundIcon,
  LockIcon,
  ShieldCheckIcon,
  UsersIcon,
  WebhookIcon,
  type LucideIcon,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { C, PublicPage } from './layout'

// Every statement here must be true of the product today. Plans are labelled as plans.
// Say what is protected, not how: no algorithms, schedules, limits or infrastructure
// details that would help an attacker plan around them.

const areas: { icon: LucideIcon; title: string; points: ReactNode[] }[] = [
  {
    icon: LockIcon,
    title: 'Encryption',
    points: [
      'All traffic is encrypted in transit with HTTPS, and browsers are told never to use plain HTTP.',
      'Secrets you give us (provider signing secrets, destination secrets, alert URLs, connection tokens) are encrypted at rest, with a separate key for each organization.',
      'Passwords, API keys and session tokens are stored only in hashed form: we cannot read them back.',
    ],
  },
  {
    icon: EyeOffIcon,
    title: 'Your payloads',
    points: [
      'Event payloads are stored as received, because replay must resend the exact bytes, and deleted after 30 days.',
      'Sensitive fields (card numbers, tokens, passwords and similar) are masked whenever an event is shown in the dashboard or API.',
      <><C>Authorization</C> and <C>Cookie</C> headers are dropped when an event arrives and never stored.</>,
    ],
  },
  {
    icon: WebhookIcon,
    title: 'Webhook security',
    points: [
      'Provider signatures are verified on arrival; failures are recorded and can alert you.',
      <>Every request we forward is signed with your destination's own secret, with a timestamp so captured requests can't be replayed later (<Link to="/docs#receive" className="text-l-accent underline-offset-4 hover:underline">how to verify</Link>).</>,
      'Destinations must be public HTTPS addresses; private and internal addresses are refused.',
    ],
  },
  {
    icon: UsersIcon,
    title: 'Access control',
    points: [
      'Owner, admin and member roles, enforced on every API route.',
      'API keys belong to one organization and carry a role. Organizations are fully isolated from one another.',
      'Every change is recorded in an audit log: who made it, what changed, when, and the result.',
    ],
  },
  {
    icon: GaugeIcon,
    title: 'Abuse protection',
    points: [
      'Sign-in, sign-up, the API and webhook URLs are rate-limited, and sign-in is protected against password guessing.',
      'The dashboard is protected against script injection and clickjacking.',
    ],
  },
  {
    icon: DatabaseBackupIcon,
    title: 'Backups and recovery',
    points: ['Your data is backed up automatically, and we regularly test restoring it.'],
  },
  {
    icon: ActivityIcon,
    title: 'Monitoring',
    points: [
      <>The platform is monitored around the clock, and problems alert our team. Public health: <Link to="/status" className="text-l-accent underline-offset-4 hover:underline">system status</Link>.</>,
    ],
  },
  {
    icon: FileClockIcon,
    title: 'Data retention',
    points: [
      'Event payloads, their deliveries and contract findings: 30 days. The alert log: 90 days.',
      'Incidents, contracts, repair rules and the audit log are kept for the life of your account.',
    ],
  },
]

export function SecurityPage() {
  return (
    <PublicPage
      eyebrow="Security"
      title="How Relaya protects your data"
      intro="Relaya sits in the path of your integrations, so it has to be the most careful part of it."
    >
      <div className="grid grid-cols-1 gap-5 md:grid-cols-2">
        {areas.map((a) => (
          <section key={a.title} className="rounded-2xl border border-l-border bg-l-surface p-6">
            <div className="mb-4 flex items-center gap-3">
              <div className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-[#14b886]/10 text-l-accent">
                <a.icon className="size-5" />
              </div>
              <h2 className="text-lg font-semibold text-l-text">{a.title}</h2>
            </div>
            <ul className="space-y-2.5 text-sm leading-relaxed">
              {a.points.map((p, i) => (
                <li key={i} className="flex gap-2.5">
                  <ShieldCheckIcon className="mt-0.5 size-4 shrink-0 text-l-accent" aria-hidden="true" />
                  <span className="min-w-0">{p}</span>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>

      <section className="mt-5 rounded-2xl border border-l-border bg-l-soft p-6">
        <div className="mb-3 flex items-center gap-3">
          <KeyRoundIcon className="size-5 text-l-accent" />
          <h2 className="text-lg font-semibold text-l-text">Security reviews</h2>
        </div>
        <p className="text-sm leading-relaxed">
          SOC 2 and ISO 27001 certification are on our roadmap. If your security review needs more detail, email{' '}
          <a href="mailto:info@relaya.sbs" className="text-l-accent underline-offset-4 hover:underline">
            info@relaya.sbs
          </a>{' '}
          and we'll answer your questionnaire. To report a vulnerability, write to the same address.
        </p>
      </section>
    </PublicPage>
  )
}
