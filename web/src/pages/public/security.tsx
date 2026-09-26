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

const areas: { icon: LucideIcon; title: string; points: ReactNode[] }[] = [
  {
    icon: LockIcon,
    title: 'Encryption',
    points: [
      'All traffic uses HTTPS; browsers are told to never use plain HTTP (HSTS).',
      'Secrets you give us (provider signing secrets, destination secrets, Slack URLs) are encrypted with AES-256-GCM, using a separate data key per organization that is itself encrypted with a master key kept outside the database.',
      'Passwords are hashed with bcrypt. API keys and session tokens are stored only as SHA-256 hashes: we cannot read them back.',
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
      'Provider signatures are verified on arrival (Razorpay, Cashfree, PayU, PhonePe, Stripe, Shopify, GitHub, Standard Webhooks / Svix, or any HMAC); failures are recorded and can alert you.',
      <>Every request we forward is signed with your destination's own secret, with a timestamp so captured requests can't be replayed later (<Link to="/docs#receive" className="text-l-accent underline-offset-4 hover:underline">how to verify</Link>).</>,
      'Destinations must be public HTTPS addresses. Private, internal and cloud-metadata addresses are refused, checked again at connection time, and redirects are never followed.',
    ],
  },
  {
    icon: UsersIcon,
    title: 'Access control',
    points: [
      'Owner, admin and member roles, enforced on every API route. An automated test tries every route with every role and fails the build if one is unprotected.',
      "API keys belong to one organization and carry a role. Another organization's data always answers \"not found\", so it can't even be probed.",
      'Every change is written to an audit log (who, what, when, from where, and the result) in the same transaction as the change itself.',
    ],
  },
  {
    icon: GaugeIcon,
    title: 'Abuse protection',
    points: [
      'Sign-in is rate-limited per address and per account (failed attempts only, so an attacker cannot lock you out by guessing).',
      'Sign-up, the API and each webhook URL have rate limits; over-limit requests get 429 with Retry-After, which providers honour.',
      'A strict Content-Security-Policy and anti-framing headers protect the dashboard against script injection and clickjacking.',
    ],
  },
  {
    icon: DatabaseBackupIcon,
    title: 'Backups and recovery',
    points: [
      'The database is backed up every night, with checksums; the last 14 are kept.',
      'Restore drills restore a real backup, compare it table by table and time it, against a target of under an hour.',
    ],
  },
  {
    icon: ActivityIcon,
    title: 'Monitoring',
    points: [
      <>Delivery, contract checks, alerts, the API and the server are monitored continuously, with alerts on stuck work, errors, low disk and missing backups. Public health: <Link to="/status" className="text-l-accent underline-offset-4 hover:underline">system status</Link>.</>,
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
      intro="Relaya sits in the path of your integrations, so it has to be the most careful part of it. This is what we do today."
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
          <h2 className="text-lg font-semibold text-l-text">Certifications</h2>
        </div>
        <p className="text-sm leading-relaxed">
          Relaya is not certified yet. An independent penetration test comes before general availability, and SOC 2 and ISO 27001 are planned
          for our second year. If your security review needs more detail, ask us: we'll answer your questionnaire.
        </p>
      </section>
    </PublicPage>
  )
}
