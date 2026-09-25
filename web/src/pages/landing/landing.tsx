import {
  ArrowRightIcon,
  CheckIcon,
  EyeOffIcon,
  FileSearchIcon,
  FingerprintIcon,
  GitCompareArrowsIcon,
  HammerIcon,
  KeyRoundIcon,
  LockIcon,
  MenuIcon,
  NetworkIcon,
  RotateCcwIcon,
  ScrollTextIcon,
  ShieldCheckIcon,
  UsersIcon,
  WebhookIcon,
  XIcon,
  type LucideIcon,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { LogoMark } from '@/components/logo'
import { ThemeToggle } from '@/components/theme-toggle'
import { cn } from '@/lib/utils'
import { Reveal, ScrollProgress, SpotlightCard, TiltCard } from './motion'
import { useScroll } from './use-scroll'
import { CodeDemo, OutLine, TypedWindow } from './code-demo'
import { rejectedExample, sendExample } from './code-samples'
import { K, MiniChart, MiniEvents, N, S } from './visuals'

// Landing page for signed-out visitors. Colors come from the --l-* tokens in
// index.css (light + dark, switched by the same theme toggle as the dashboard).
// Product visuals (incident card, code) stay dark in both themes, like screenshots.
// Features that don't exist yet are labelled "Coming soon": this page must never
// promise what the product can't do today.

const TRIAL_DAYS = 7

const nav = [
  { href: '#how', label: 'How it works' },
  { href: '#features', label: 'Features' },
  { href: '#security', label: 'Security' },
  { href: '#pricing', label: 'Pricing' },
]

export function LandingPage() {
  const { progress, scrolled } = useScroll()
  return (
    <div className="min-h-svh overflow-x-clip bg-l-bg text-l-muted antialiased transition-colors duration-300">
      <ScrollProgress progress={progress} />
      <Header scrolled={scrolled} />
      <main>
        <Hero />
        <WorksWith />
        <FourQuestions />
        <HowItWorks />
        <Features />
        <BuildMissing />
        <Developers />
        <Security />
        <Pricing />
        <FinalCta />
      </main>
      <Footer />
    </div>
  )
}

// ---- building blocks --------------------------------------------------------------

function PrimaryCta({ to = '/signup', children, className }: { to?: string; children: ReactNode; className?: string }) {
  return (
    <Link
      to={to}
      className={cn(
        'group inline-flex h-11 items-center justify-center gap-2 rounded-lg bg-[#14b886] px-5 text-sm font-semibold text-[#0b1520] shadow-lg shadow-[#14b886]/25 transition duration-200',
        'hover:-translate-y-0.5 hover:bg-[#2fd09d] hover:shadow-xl hover:shadow-[#14b886]/35 active:translate-y-0 motion-reduce:hover:translate-y-0',
        'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#14b886]',
        className,
      )}
    >
      {children}
    </Link>
  )
}

function SecondaryCta({ to, href, children, className }: { to?: string; href?: string; children: ReactNode; className?: string }) {
  const cls = cn(
    'group inline-flex h-11 items-center justify-center gap-2 rounded-lg border border-l-border px-5 text-sm font-semibold text-l-text transition duration-200 hover:-translate-y-0.5 hover:border-[#14b886]/50 hover:bg-l-soft motion-reduce:hover:translate-y-0',
    className,
  )
  return to ? (
    <Link to={to} className={cls}>
      {children}
    </Link>
  ) : (
    <a href={href} className={cls}>
      {children}
    </a>
  )
}

/** Arrow that nudges right when its group (a button) is hovered. */
const Arrow = () => <ArrowRightIcon className="size-4 transition-transform duration-200 group-hover:translate-x-1" />

function Section({ id, eyebrow, title, intro, children, className }: {
  id?: string
  eyebrow?: string
  title: ReactNode
  intro?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section id={id} className={cn('scroll-mt-20 px-4 py-20 sm:px-6 md:py-28', className)}>
      <div className="mx-auto max-w-6xl">
        <Reveal className="mx-auto mb-12 max-w-2xl text-center md:mb-16">
          {eyebrow && <div className="mb-3 text-xs font-semibold uppercase tracking-[0.2em] text-l-accent">{eyebrow}</div>}
          <h2 className="text-balance text-3xl font-semibold tracking-tight text-l-text md:text-4xl">{title}</h2>
          {intro && <p className="mt-4 text-pretty text-base md:text-lg">{intro}</p>}
        </Reveal>
        {children}
      </div>
    </section>
  )
}

function Status({ live }: { live: boolean }) {
  return live ? (
    <span className="inline-flex items-center gap-1.5 rounded-full bg-[#14b886]/15 px-2 py-0.5 text-[11px] font-medium text-l-accent">
      <span className="relative flex size-1.5">
        <span className="absolute inline-flex size-full rounded-full bg-[#14b886] opacity-70 motion-safe:animate-ping" />
        <span className="relative inline-flex size-1.5 rounded-full bg-[#14b886]" />
      </span>
      Live
    </span>
  ) : (
    <span className="inline-flex items-center rounded-full border border-l-border px-2 py-0.5 text-[11px] font-medium text-l-subtle">
      Coming soon
    </span>
  )
}

/** Icon tile that tilts slightly when its card is hovered. */
function IconTile({ icon: Icon }: { icon: LucideIcon }) {
  return (
    <div className="flex size-10 items-center justify-center rounded-xl bg-[#14b886]/10 text-l-accent transition duration-300 group-hover:-rotate-6 group-hover:scale-110 group-hover:bg-[#14b886]/20">
      <Icon className="size-5" />
    </div>
  )
}

// ---- header -----------------------------------------------------------------------

function Header({ scrolled }: { scrolled: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <header
      className={cn(
        'sticky top-0 z-40 border-b backdrop-blur-md transition-all duration-300',
        scrolled || open ? 'border-l-border bg-l-bg/85 shadow-[0_8px_30px_-12px_rgb(0_0_0_/_0.25)]' : 'border-transparent bg-transparent',
      )}
    >
      <div className="mx-auto flex h-16 max-w-6xl items-center gap-6 px-4 sm:px-6">
        <Link to="/" className="group flex items-center gap-2.5" aria-label="Relaya home">
          <LogoMark className="size-8 transition-transform duration-300 group-hover:-rotate-6 group-hover:scale-105" />
          <span className="text-sm font-semibold tracking-[0.2em] text-l-text">RELAYA</span>
        </Link>
        <nav className="hidden items-center gap-1 text-sm md:flex">
          {nav.map((n) => (
            <a
              key={n.href}
              href={n.href}
              className="relative rounded-md px-3 py-1.5 transition after:absolute after:inset-x-3 after:-bottom-0.5 after:h-px after:origin-left after:scale-x-0 after:bg-[#14b886] after:transition-transform after:duration-300 hover:text-l-text hover:after:scale-x-100"
            >
              {n.label}
            </a>
          ))}
        </nav>
        <div className="ml-auto hidden items-center gap-3 md:flex">
          <ThemeToggle />
          <Link to="/login" className="px-2 text-sm font-medium text-l-text/80 transition hover:text-l-text">
            Log in
          </Link>
          <SecondaryCta to="/signup" className="h-9 px-4">
            Sign up
          </SecondaryCta>
          <PrimaryCta className="hidden h-9 px-4 lg:inline-flex">Start free trial</PrimaryCta>
        </div>
        <button
          type="button"
          className="ml-auto flex size-10 items-center justify-center rounded-lg text-l-text transition hover:bg-l-soft md:hidden"
          aria-label={open ? 'Close menu' : 'Open menu'}
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          {open ? <XIcon className="size-5" /> : <MenuIcon className="size-5" />}
        </button>
      </div>
      {open && (
        <div className="border-t border-l-border px-4 pb-5 pt-2 motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-top-2 md:hidden">
          <nav className="flex flex-col">
            {nav.map((n) => (
              <a key={n.href} href={n.href} onClick={() => setOpen(false)} className="py-3 text-base text-l-text/85">
                {n.label}
              </a>
            ))}
          </nav>
          <div className="mt-2 flex items-center justify-between border-t border-l-border py-3">
            <span className="text-sm">Theme</span>
            <ThemeToggle />
          </div>
          <div className="mt-2 grid grid-cols-2 gap-3">
            <SecondaryCta to="/login">Log in</SecondaryCta>
            <SecondaryCta to="/signup">Sign up</SecondaryCta>
            <PrimaryCta className="col-span-2">Start free trial</PrimaryCta>
          </div>
        </div>
      )}
    </header>
  )
}

// ---- hero -------------------------------------------------------------------------

function Hero() {
  return (
    <section className="relative -mt-16 overflow-hidden px-4 pb-20 pt-28 sm:px-6 md:pb-28 md:pt-40">
      <div
        className="pointer-events-none absolute inset-0 bg-[linear-gradient(to_right,var(--l-grid)_1px,transparent_1px),linear-gradient(to_bottom,var(--l-grid)_1px,transparent_1px)] bg-[size:48px_48px] [mask-image:radial-gradient(ellipse_at_top,black_30%,transparent_75%)]"
        aria-hidden="true"
      />
      <div className="pointer-events-none absolute -top-40 left-1/2 h-[500px] w-[900px] -translate-x-1/2 rounded-full bg-[#14b886]/15 blur-3xl" aria-hidden="true" />
      <div className="relative mx-auto grid max-w-6xl items-center gap-14 lg:grid-cols-[1.1fr_1fr]">
        <div>
          <Reveal>
            <a
              href="#pricing"
              className="group mb-6 inline-flex items-center gap-2 rounded-full border border-[#14b886]/30 bg-[#14b886]/10 px-3 py-1 text-xs font-medium text-l-accent transition hover:border-[#14b886]/60 hover:bg-[#14b886]/15"
            >
              Design partner program open · 50% off for 12 months
              <ArrowRightIcon className="size-3.5 transition-transform group-hover:translate-x-0.5" />
            </a>
          </Reveal>
          <Reveal delay={80}>
            <h1 className="text-balance text-4xl font-semibold leading-[1.08] tracking-tight text-l-text sm:text-5xl md:text-6xl">
              Integrations break.
              <br />
              <span className="l-shimmer bg-gradient-to-r from-[var(--l-grad-a)] via-[var(--l-grad-b)] to-[var(--l-grad-a)] bg-clip-text text-transparent">
                Know first.
                <br />
                Fix safely.
              </span>
            </h1>
          </Reveal>
          <Reveal delay={160}>
            <p className="mt-6 max-w-xl text-pretty text-lg">
              Relaya verifies and records every webhook your product receives, flags bad signatures and integrations that go
              quiet, and gives you the evidence to fix problems before customers notice. Missing an integration? We build it.
            </p>
          </Reveal>
          <Reveal delay={240}>
            <div className="mt-8 flex flex-wrap items-center gap-3">
              <PrimaryCta>
                Start free trial <Arrow />
              </PrimaryCta>
              <SecondaryCta href="#how">See how it works</SecondaryCta>
            </div>
            <p className="mt-4 text-xs text-l-subtle">{TRIAL_DAYS}-day free trial of the Team plan · first webhook in under 15 minutes</p>
          </Reveal>
        </div>
        <Reveal delay={200}>
          <div className="relative">
            <div className="pointer-events-none absolute -inset-6 rounded-[2rem] bg-[radial-gradient(ellipse_at_top,rgba(20,184,134,0.28),transparent_65%)] blur-2xl" aria-hidden="true" />
            <div className="l-float relative">
              <TiltCard>
                <CodeDemo />
              </TiltCard>
            </div>
          </div>
          <p className="mt-4 text-center text-xs text-l-subtle">Illustrative. Impact, replay and verify in the incident view are coming soon.</p>
        </Reveal>
      </div>
    </section>
  )
}

function WorksWith() {
  const live = ['Razorpay', 'Stripe', 'Shopify', 'GitHub', 'Any HMAC webhook']
  const next = ['Zoho', 'HubSpot', 'Tally', 'Shiprocket', 'Keka']
  const items = [...live.map((name) => ({ name, live: true })), ...next.map((name) => ({ name, live: false }))]
  return (
    <div className="border-y border-l-border bg-l-soft py-6">
      <div className="mx-auto flex max-w-6xl flex-col items-center gap-4 px-4 sm:px-6 md:flex-row">
        <span className="shrink-0 text-sm text-l-subtle">Verifies signatures from</span>
        <div className="relative w-full overflow-hidden [contain:paint] [mask-image:linear-gradient(to_right,transparent,black_12%,black_88%,transparent)]">
          <div className="l-marquee flex w-max gap-3">
            {[...items, ...items].map((p, i) => (
              <span
                key={i}
                aria-hidden={i >= items.length}
                className={cn(
                  'inline-flex items-center gap-2 whitespace-nowrap rounded-full border px-3.5 py-1.5 text-sm font-medium transition hover:border-[#14b886]/50',
                  p.live ? 'border-l-border bg-l-bg text-l-text' : 'border-dashed border-l-border text-l-subtle',
                )}
              >
                {p.live ? <CheckIcon className="size-3.5 text-l-accent" /> : <span className="text-[11px]">on request</span>}
                {p.name}
              </span>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}

// ---- four questions --------------------------------------------------------------------

function FourQuestions() {
  const qs: { icon: LucideIcon; q: string; a: string; live: boolean }[] = [
    { icon: FileSearchIcon, q: 'What broke?', a: 'Every delivery stored with headers, payload and signature result. Filter by provider, event type or status in seconds.', live: true },
    { icon: UsersIcon, q: 'Who is affected?', a: 'Trace a failure from provider to connection to customer, with counts and the money involved.', live: false },
    { icon: RotateCcwIcon, q: 'Is it safe to fix?', a: 'Dry-run first, idempotency keys on every replay, approval for bulk actions. Never retry blindly.', live: false },
    { icon: ShieldCheckIcon, q: 'Did the fix work?', a: 'Read the result back from the destination and close the incident with proof, not hope.', live: false },
  ]
  return (
    <Section
      eyebrow="Why Relaya"
      title="Builders stop at “it's connected”. Monitoring stops at “there was an error”."
      intro="During an integration incident your team asks four questions. Relaya is built to answer all of them, in order."
    >
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {qs.map((x, i) => (
          <Reveal key={x.q} delay={i * 100}>
            <SpotlightCard className="p-6">
              <div className="mb-4 flex items-center justify-between">
                <IconTile icon={x.icon} />
                <span className="font-mono text-xs text-l-subtle transition group-hover:text-l-accent">0{i + 1}</span>
              </div>
              <h3 className="text-lg font-semibold text-l-text">{x.q}</h3>
              <p className="mt-2 text-sm leading-relaxed">{x.a}</p>
              <div className="mt-4">
                <Status live={x.live} />
              </div>
            </SpotlightCard>
          </Reveal>
        ))}
      </div>
    </Section>
  )
}

// ---- how it works ----------------------------------------------------------------------

function HowItWorks() {
  return (
    <Section
      id="how"
      eyebrow="How it works"
      title="Protect your first integration in 15 minutes"
      intro="No SDK to install and no code to change. Point a webhook at Relaya and it starts watching."
      className="bg-l-soft"
    >
      <div className="relative grid gap-10 lg:grid-cols-3 lg:gap-8">
        {/* connecting line between the steps on desktop */}
        <div className="pointer-events-none absolute left-[16%] right-[16%] top-4 hidden h-px bg-gradient-to-r from-transparent via-[#14b886]/40 to-transparent lg:block" aria-hidden="true" />
        <Reveal>
          <Step n="01" title="Create a webhook URL" body="Pick the provider, paste its signing secret. The secret is encrypted with a key unique to your organization.">
            <div className="rounded-xl border border-white/10 bg-[#0b1520] p-4 transition duration-300 hover:-translate-y-1 hover:border-[#14b886]/40">
              <div className="mb-2 text-xs text-slate-500">Webhook URL for Razorpay production</div>
              <div className="flex items-center gap-2">
                <div className="min-w-0 flex-1 truncate rounded-md border border-white/10 bg-white/5 px-3 py-2 font-mono text-xs text-slate-200">
                  https://in.relaya…/v1/in/in_7Kq…
                </div>
                <span className="rounded-md border border-white/10 px-2 py-2 text-xs text-slate-300">Copy</span>
              </div>
            </div>
          </Step>
        </Reveal>
        <Reveal delay={150}>
          <Step n="02" title="Every delivery is verified and stored" body="Signatures checked, duplicates dropped, sensitive fields masked. Forged requests are rejected and kept as evidence.">
            <div className="transition duration-300 hover:-translate-y-1">
              <MiniEvents />
            </div>
          </Step>
        </Reveal>
        <Reveal delay={300}>
          <Step n="03" title="See problems before customers do" body="Overview shows traffic and health per webhook. Signature failures and silent drops stand out immediately.">
            <div className="transition duration-300 hover:-translate-y-1">
              <MiniChart />
            </div>
          </Step>
        </Reveal>
      </div>
    </Section>
  )
}

function Step({ n, title, body, children }: { n: string; title: string; body: string; children: ReactNode }) {
  return (
    <div className="flex h-full flex-col gap-5">
      <div>
        <div className="relative mb-4 flex size-8 items-center justify-center rounded-full border border-[#14b886]/40 bg-l-bg font-mono text-xs font-medium text-l-accent">
          {n}
        </div>
        <h3 className="text-xl font-semibold text-l-text">{title}</h3>
        <p className="mt-2 text-sm leading-relaxed">{body}</p>
      </div>
      <div className="mt-auto">{children}</div>
    </div>
  )
}

// ---- features ----------------------------------------------------------------------------

function Features() {
  const items: { icon: LucideIcon; title: string; body: string; live: boolean }[] = [
    { icon: WebhookIcon, title: 'Webhook gateway', body: 'A separate, tiny ingest path that verifies, stores and answers in milliseconds. The dashboard can go down; ingest keeps running.', live: true },
    { icon: FingerprintIcon, title: 'Signature verification', body: 'Razorpay, Stripe, Shopify, GitHub and generic HMAC out of the box. Stripe timestamps checked against replay attacks.', live: true },
    { icon: FileSearchIcon, title: 'Event explorer', body: 'Search by provider event ID, type, status or signature. Full headers and payload, with secrets masked.', live: true },
    { icon: GitCompareArrowsIcon, title: 'Integration contracts', body: 'Learns the shape of each payload and flags a renamed, removed or retyped field as a breaking change.', live: false },
    { icon: NetworkIcon, title: 'Impact analysis', body: 'Provider → integration → connection → customer. Know exactly who a failure touches.', live: false },
    { icon: RotateCcwIcon, title: 'Safe replay & verify', body: 'Retry with backoff, dry-run before replay, idempotency keys, and a read-back that proves the fix.', live: false },
  ]
  return (
    <Section
      id="features"
      eyebrow="Features"
      title="One control plane for every integration you run"
      intro="Starting with webhooks from any provider. Custom-built integrations and platforms like Nango come next."
    >
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {items.map((f, i) => (
          <Reveal key={f.title} delay={(i % 3) * 100}>
            <SpotlightCard className="p-6 md:p-7">
              <div className="mb-5 flex items-center justify-between">
                <IconTile icon={f.icon} />
                <Status live={f.live} />
              </div>
              <h3 className="font-semibold text-l-text">{f.title}</h3>
              <p className="mt-2 text-sm leading-relaxed">{f.body}</p>
            </SpotlightCard>
          </Reveal>
        ))}
      </div>
    </Section>
  )
}

function BuildMissing() {
  return (
    <section className="px-4 sm:px-6">
      <Reveal className="mx-auto max-w-6xl">
        <div className="group relative overflow-hidden rounded-3xl border border-[#14b886]/30 bg-gradient-to-br from-[#14b886]/15 via-[#14b886]/5 to-transparent p-8 transition duration-500 hover:border-[#14b886]/60 md:p-14">
          <div className="pointer-events-none absolute -right-20 -top-20 size-72 rounded-full bg-[#14b886]/20 blur-3xl transition duration-700 group-hover:scale-125" aria-hidden="true" />
          <div className="relative grid items-center gap-10 md:grid-cols-[1.3fr_1fr]">
            <div>
              <div className="mb-4 flex size-11 items-center justify-center rounded-xl bg-[#14b886]/20 text-l-accent transition duration-500 group-hover:-rotate-12">
                <HammerIcon className="size-5" />
              </div>
              <h2 className="text-balance text-2xl font-semibold tracking-tight text-l-text md:text-3xl">
                The integration you need doesn't exist? We build it.
              </h2>
              <p className="mt-4 text-pretty">
                Tally, a regional logistics API, a niche HRMS: tell us what you need and we build it on the Relaya connector
                standard. It's protected from day one, with verification, retries and monitoring built in.
              </p>
            </div>
            <ul className="space-y-3 text-sm">
              {['Built and maintained by our team', 'Protected by the same checks as every other integration', 'Delivered in 1 to 3 weeks', 'Free if it becomes part of the public catalog'].map(
                (t) => (
                  <li key={t} className="flex items-start gap-3 text-l-text/90">
                    <span className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-[#14b886]/20">
                      <CheckIcon className="size-3 text-l-accent" />
                    </span>
                    {t}
                  </li>
                ),
              )}
            </ul>
          </div>
        </div>
      </Reveal>
    </section>
  )
}

// ---- developers --------------------------------------------------------------------------

function Developers() {
  return (
    <Section eyebrow="For developers" title="API-first, like the rest of your stack" intro="Everything in the dashboard is available over a REST API with scoped API keys.">
      <div className="grid gap-6 lg:grid-cols-2">
        <Reveal>
          <div className="transition duration-300 hover:-translate-y-1">
            <TypedWindow
              title="send a test delivery"
              lines={sendExample}
              output={
                <>
                  <OutLine className="text-slate-500"># 200 OK in milliseconds</OutLine>
                  <OutLine delay={200} className="text-slate-300">
                    {'{ '}<K>"id"</K>: <S>"c55817d8-…"</S>, <K>"duplicate"</K>: <N>false</N>{' }'}
                  </OutLine>
                </>
              }
            />
          </div>
        </Reveal>
        <Reveal delay={150}>
          <div className="transition duration-300 hover:-translate-y-1">
            <TypedWindow
              title="find every rejected delivery"
              lines={rejectedExample}
              output={
                <>
                  <OutLine className="text-slate-300">{'{ '}<K>"data"</K>: [{'{'}</OutLine>
                  <OutLine delay={150} className="text-slate-300">{'    '}<K>"type"</K>: <S>"payment.captured"</S>, <K>"signature"</K>: <S>"invalid"</S>,</OutLine>
                  <OutLine delay={300} className="text-slate-300">{'    '}<K>"received_at"</K>: <S>"2026-09-25T07:33:23Z"</S></OutLine>
                  <OutLine delay={450} className="text-slate-300">{'  }'}], <K>"next_cursor"</K>: <N>null</N>{' }'}</OutLine>
                </>
              }
            />
          </div>
        </Reveal>
      </div>
    </Section>
  )
}

// ---- security ----------------------------------------------------------------------------

function Security() {
  const items: { icon: LucideIcon; title: string; body: string }[] = [
    { icon: LockIcon, title: 'Envelope encryption', body: 'Signing secrets and tokens are encrypted with a separate data key per organization.' },
    { icon: EyeOffIcon, title: 'Masked by default', body: 'Card numbers, tokens and passwords are masked in the explorer. Credential headers are never stored.' },
    { icon: KeyRoundIcon, title: 'Roles & API keys', body: 'Owner, admin and member roles. Scoped API keys that are shown once and can be revoked instantly.' },
    { icon: ScrollTextIcon, title: 'Audit log', body: 'Every change records who did it, to what, and the result, written in the same transaction as the change.' },
  ]
  return (
    <Section id="security" eyebrow="Security" title="You're trusting us with production traffic. We take that seriously." className="bg-l-soft">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {items.map((s, i) => (
          <Reveal key={s.title} delay={i * 100}>
            <SpotlightCard className="p-6">
              <div className="mb-4">
                <IconTile icon={s.icon} />
              </div>
              <h3 className="font-semibold text-l-text">{s.title}</h3>
              <p className="mt-2 text-sm leading-relaxed">{s.body}</p>
            </SpotlightCard>
          </Reveal>
        ))}
      </div>
    </Section>
  )
}

// ---- pricing -----------------------------------------------------------------------------


function Pricing() {
  const plans = [
    { name: 'Team', price: '₹14,999', per: '/ month', blurb: 'For teams with integrations in production.', items: ['500 connections', '1M events / month', '30-day history', 'Contracts, incidents & replay', 'Roles, API keys, audit log'], cta: 'Start free trial', highlight: true },
    { name: 'Business', price: '₹49,999', per: '/ month', blurb: 'For integrations that move money or customer data.', items: ['5,000 connections', '10M events / month', 'Impact analysis', 'Dry-run & verify', 'Audit export'], cta: 'Start with a trial', highlight: false },
    { name: 'Enterprise', price: '₹2 lakh+', per: '/ month', blurb: 'For large teams with security and compliance needs.', items: ['Custom connection & event limits', 'SSO and SCIM', 'Self-hosted option', 'Uptime SLA', '1 custom connector every quarter'], cta: 'Start with a trial', highlight: false },
  ]
  return (
    <Section
      id="pricing"
      eyebrow="Pricing"
      title="Simple plans that grow with your integrations"
      intro={`Try the Team plan free for ${TRIAL_DAYS} days. Design partners get 50% off any plan for 12 months in exchange for feedback.`}
    >
      <div className="grid items-stretch gap-4 lg:grid-cols-3">
        {plans.map((p, i) => (
          <Reveal key={p.name} delay={i * 120}>
            <SpotlightCard className={cn('p-6 md:p-8', p.highlight && 'border-[#14b886]/50 bg-[#14b886]/[0.06]')}>
              <div className="flex h-full flex-col">
                <div className="flex items-center justify-between">
                  <h3 className="font-semibold text-l-text">{p.name}</h3>
                  {p.highlight && <span className="rounded-full bg-[#14b886] px-2 py-0.5 text-[11px] font-semibold text-[#0b1520]">Most popular</span>}
                </div>
                <div className="mt-4 flex items-baseline gap-1">
                  <span className="text-4xl font-semibold tracking-tight text-l-text">{p.price}</span>
                  <span className="text-sm text-l-subtle">{p.per}</span>
                </div>
                <p className="mt-2 text-sm">{p.blurb}</p>
                <ul className="my-6 space-y-2.5 text-sm">
                  {p.items.map((it) => (
                    <li key={it} className="flex items-start gap-2.5 text-l-text/90">
                      <CheckIcon className="mt-0.5 size-4 shrink-0 text-l-accent" />
                      {it}
                    </li>
                  ))}
                </ul>
                <div className="mt-auto grid">
                  {p.highlight ? (
                    <PrimaryCta>
                      {p.cta} <Arrow />
                    </PrimaryCta>
                  ) : (
                    <SecondaryCta to="/signup">{p.cta}</SecondaryCta>
                  )}
                </div>
              </div>
            </SpotlightCard>
          </Reveal>
        ))}
      </div>
      <Reveal>
        <p className="mx-auto mt-8 max-w-3xl text-center text-sm text-l-subtle">
          {TRIAL_DAYS}-day free trial of the Team plan, card required. You're charged when the trial ends; cancel before
          then and you pay nothing. Plans include features as they launch; items marked Coming soon are on the way.
          Custom connectors from ₹50k one-time.
        </p>
      </Reveal>
    </Section>
  )
}

function FinalCta() {
  return (
    <section className="px-4 pb-24 sm:px-6">
      <Reveal className="mx-auto max-w-6xl">
        <div className="group relative overflow-hidden rounded-3xl border border-l-border bg-l-surface px-6 py-16 text-center md:py-20">
          <div className="pointer-events-none absolute inset-x-0 -top-24 mx-auto h-64 max-w-2xl rounded-full bg-[#14b886]/20 blur-3xl transition duration-700 group-hover:bg-[#14b886]/30" aria-hidden="true" />
          <div className="relative">
            <LogoMark className="mx-auto mb-6 size-12 transition duration-500 group-hover:-rotate-6 group-hover:scale-110" />
            <h2 className="text-balance text-3xl font-semibold tracking-tight text-l-text md:text-4xl">Stop finding out from your customers.</h2>
            <p className="mx-auto mt-4 max-w-xl text-pretty">
              Point your first webhook at Relaya today. Try it free for {TRIAL_DAYS} days; setup takes about as long as reading this page.
            </p>
            <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
              <PrimaryCta>
                Start free trial <Arrow />
              </PrimaryCta>
              <SecondaryCta to="/login">Log in</SecondaryCta>
            </div>
          </div>
        </div>
      </Reveal>
    </section>
  )
}

function Footer() {
  return (
    <footer className="border-t border-l-border px-4 py-12 sm:px-6">
      <div className="mx-auto flex max-w-6xl flex-col gap-8 md:flex-row md:items-start md:justify-between">
        <div className="max-w-xs">
          <div className="flex items-center gap-2.5">
            <LogoMark className="size-7" />
            <span className="text-sm font-semibold tracking-[0.2em] text-l-text">RELAYA</span>
          </div>
          <p className="mt-3 text-sm text-l-subtle">The control plane for integrations: know first, fix safely.</p>
        </div>
        <div className="grid grid-cols-2 gap-10 text-sm">
          <div>
            <div className="mb-3 font-medium text-l-text">Product</div>
            <ul className="space-y-2">
              {nav.map((n) => (
                <li key={n.href}>
                  <a href={n.href} className="transition hover:text-l-text">
                    {n.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
          <div>
            <div className="mb-3 font-medium text-l-text">Account</div>
            <ul className="space-y-2">
              <li>
                <Link to="/signup" className="transition hover:text-l-text">
                  Sign up
                </Link>
              </li>
              <li>
                <Link to="/login" className="transition hover:text-l-text">
                  Log in
                </Link>
              </li>
            </ul>
          </div>
        </div>
      </div>
      <div className="mx-auto mt-10 flex max-w-6xl items-center justify-between gap-4 border-t border-l-border pt-6 text-xs text-l-subtle">
        <span>© {new Date().getFullYear()} Relaya. Made in India.</span>
        <ThemeToggle />
      </div>
    </footer>
  )
}
