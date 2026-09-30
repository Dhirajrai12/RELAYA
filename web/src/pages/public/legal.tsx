import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { PublicPage } from './layout'

// ⚠ Fill in the email and city before launch, and have a lawyer review both pages.
// Everything else below describes the product as it works today; keep it true when features change.
export const LEGAL = {
  entity: 'Relaya', // the company name
  email: '[contact email to be added]', // for legal, privacy and data requests
  city: '[city]', // courts with jurisdiction, e.g. Bengaluru
  updated: '29 September 2026',
}

function Doc({ sections }: { sections: { title: string; body: ReactNode }[] }) {
  return (
    <article className="max-w-3xl text-[15px] leading-relaxed">
      <p className="mb-8 text-sm text-l-subtle">Last updated: {LEGAL.updated}</p>
      <ol className="space-y-8">
        {sections.map((s, i) => (
          <li key={s.title} id={s.title.toLowerCase().replace(/[^a-z]+/g, '-')} className="scroll-mt-24">
            <h2 className="mb-3 text-xl font-semibold tracking-tight text-l-text">
              {i + 1}. {s.title}
            </h2>
            <div className="space-y-3 [&_li]:ml-5 [&_li]:list-disc [&_ul]:space-y-1.5">{s.body}</div>
          </li>
        ))}
      </ol>
    </article>
  )
}

const B = ({ children }: { children: ReactNode }) => <b className="text-l-text">{children}</b>
const A = ({ to, children }: { to: string; children: ReactNode }) => (
  <Link to={to} className="text-l-accent underline-offset-4 hover:underline">
    {children}
  </Link>
)

export function TermsPage() {
  const { entity, email, city } = LEGAL
  return (
    <PublicPage
      eyebrow="Legal"
      title="Terms of Service"
      intro={`These terms are the agreement between you and ${entity} for using Relaya. Please read them; by creating an account or using the service you accept them.`}
    >
      <Doc
        sections={[
          {
            title: 'The service',
            body: (
              <>
                <p>
                  Relaya receives webhooks from providers you choose, verifies and stores them, forwards them to endpoints you configure, checks them
                  against contracts, and alerts you. It can also send webhooks to your own customers, and connect to your users' accounts at other apps
                  when they authorise it. The <A to="/docs">documentation</A> describes how each part works.
                </p>
                <p>
                  "You" means the person or organisation that created the account. If you accept these terms for a company, you confirm you may bind it.
                </p>
              </>
            ),
          },
          {
            title: 'Your account',
            body: (
              <ul>
                <li>You must be at least 18 and give accurate sign-up details.</li>
                <li>Keep your password and API keys secret. You are responsible for what happens under your account, including by your team members and keys.</li>
                <li>Tell us promptly at {email} if you think your account or a key has been misused. Revoke exposed API keys in Settings → API keys.</li>
              </ul>
            ),
          },
          {
            title: 'Acceptable use',
            body: (
              <>
                <p>You must not use Relaya to:</p>
                <ul>
                  <li>break any law, or process data you have no right to process;</li>
                  <li>send spam, malware or unsolicited messages, or attack or overload other systems (including through destinations or outbound endpoints);</li>
                  <li>try to get around rate limits, access controls or other accounts' data, or probe the service for weaknesses without our written permission;</li>
                  <li>resell or rebrand the service without an agreement with us.</li>
                </ul>
                <p>We may suspend traffic or an account that puts the service or others at risk, and will tell you why when we can.</p>
              </>
            ),
          },
          {
            title: 'Your data',
            body: (
              <>
                <p>
                  You keep all rights to the data you send through Relaya (event payloads, configuration and so on). You give us permission to store,
                  process and transmit it only as needed to run the service for you, as described in the <A to="/privacy">Privacy Policy</A> and on
                  the <A to="/security">Security</A> page.
                </p>
                <p>
                  Payloads may contain personal data about your own customers. For that data you decide what is processed and why, and we process it on
                  your behalf and on your instructions. You are responsible for having a lawful basis and any notices or consents needed to send it to us.
                </p>
                <p>Event payloads are kept for 30 days and then deleted. You can export what you need through the API before then.</p>
              </>
            ),
          },
          {
            title: 'Connected accounts and third parties',
            body: (
              <p>
                When you or your users connect an account at another app (for example Zoho, HubSpot, Google, Jira or Shiprocket), or forward data to a
                provider or endpoint, that party's own terms apply to it. We are not responsible for third-party services, their availability or what they
                do with data you send them.
              </p>
            ),
          },
          {
            title: 'Fees',
            body: (
              <p>
                The service is currently offered free of charge. Before we introduce paid plans, we will publish the prices and give you at least 30 days'
                notice; paid features only start if you choose a plan.
              </p>
            ),
          },
          {
            title: 'Availability and support',
            body: (
              <p>
                We work to keep Relaya available and to retry deliveries that fail, but we do not promise uninterrupted service, and there is no uptime
                guarantee unless we agree one with you in writing. Maintenance or incidents may cause interruptions; live status is on the{' '}
                <A to="/status">status page</A>. Keep the provider's own retries enabled, and do not rely on Relaya as the only copy of important data.
              </p>
            ),
          },
          {
            title: 'Disclaimer',
            body: (
              <p>
                To the extent the law allows, the service is provided "as is" and "as available", without warranties of any kind, including fitness for a
                particular purpose. Contract checks, repair rules and alerts help you notice and handle problems; they do not guarantee that every problem
                is detected or fixed.
              </p>
            ),
          },
          {
            title: 'Limitation of liability',
            body: (
              <p>
                To the extent the law allows, {entity} is not liable for indirect or consequential losses, lost profits, revenue or data, and our total
                liability for all claims relating to the service is limited to the greater of the fees you paid us in the 12 months before the claim and INR
                10,000. Nothing here limits liability that cannot be limited by law.
              </p>
            ),
          },
          {
            title: 'Ending the agreement',
            body: (
              <p>
                You can stop using Relaya at any time; to close your account and delete its data, write to {email}. We may end or suspend your access for a
                serious or repeated breach of these terms, or with 30 days' notice for any other reason. After an account is closed, its data is deleted
                within 30 days, and from backups when they expire (within 14 days after that).
              </p>
            ),
          },
          {
            title: 'Changes to these terms',
            body: (
              <p>
                We may update these terms. For important changes we will give notice in the dashboard or by email at least 15 days before they apply. If you
                keep using the service after that, the new terms apply; if you don't agree, you can close your account.
              </p>
            ),
          },
          {
            title: 'Law and disputes',
            body: (
              <p>
                These terms are governed by the laws of India. The courts at {city}, India, have exclusive jurisdiction over any dispute, which we will first
                try to settle in good faith by writing to each other.
              </p>
            ),
          },
          {
            title: 'Contact',
            body: <p>{entity}: {email}</p>,
          },
        ]}
      />
    </PublicPage>
  )
}

export function PrivacyPage() {
  const { entity, email } = LEGAL
  return (
    <PublicPage
      eyebrow="Legal"
      title="Privacy Policy"
      intro={`How ${entity} collects, uses and protects personal data when you use Relaya, and your rights over it. Written for India's Digital Personal Data Protection Act, 2023.`}
    >
      <Doc
        sections={[
          {
            title: 'Who we are',
            body: (
              <p>
                {entity} runs Relaya. For your account data we decide how it is used (a "data fiduciary"). For data inside the events you send through
                Relaya, you decide, and we process it on your behalf (a "data processor"). Contact: {email}.
              </p>
            ),
          },
          {
            title: 'What we collect',
            body: (
              <ul>
                <li>
                  <B>Account data:</B> your name, email address, organisation name, and your password (stored only as a one-way hash), plus the
                  memberships and roles in your organisation.
                </li>
                <li>
                  <B>Configuration:</B> projects, webhooks, destinations, alert channels, contracts and rules. Secrets (signing secrets, API tokens, Slack
                  and Jira credentials, connection tokens) are encrypted.
                </li>
                <li>
                  <B>Event data:</B> the webhooks you route through Relaya: headers and bodies as received (without <code>Authorization</code> and{' '}
                  <code>Cookie</code> headers), the sender's IP address, and delivery results. These may contain personal data about your customers.
                </li>
                <li>
                  <B>Connected accounts:</B> when your users connect an app, the access tokens and the account details that app returns, and the records a
                  sync reads.
                </li>
                <li>
                  <B>Usage and security data:</B> an audit log of changes (who, what, when), sign-in attempts, API call logs, and server logs with IP
                  addresses, used to secure and operate the service.
                </li>
              </ul>
            ),
          },
          {
            title: 'How we use it',
            body: (
              <ul>
                <li>to provide the service: receive, verify, store, forward, check and replay events, send alerts, and run connections and syncs;</li>
                <li>to keep it secure: authentication, rate limits, abuse prevention and the audit log;</li>
                <li>to operate and improve it: monitoring, debugging and capacity planning;</li>
                <li>to contact you about your account, security issues and important changes.</li>
              </ul>
            ),
          },
          {
            title: 'What we do not do',
            body: (
              <ul>
                <li>We do not sell personal data or use it for advertising.</li>
                <li>We do not read your event payloads except when needed to run the service, fix a problem you report, or meet a legal obligation.</li>
                <li>We do not use your data to train AI models.</li>
              </ul>
            ),
          },
          {
            title: 'Who we share it with',
            body: (
              <>
                <p>
                  Relaya runs on servers we operate; we do not currently use outside processors to store or process your data. Data leaves Relaya only
                  where you direct it:
                </p>
                <ul>
                  <li>to the destinations, alert channels (Slack, email, Jira, webhooks) and outbound endpoints you configure;</li>
                  <li>to the apps you or your users connect, when calls are made through a connection;</li>
                  <li>to authorities when the law requires it.</li>
                </ul>
                <p>If we start using a processor (for example an email provider), we will list it here before we do.</p>
              </>
            ),
          },
          {
            title: 'How long we keep it',
            body: (
              <ul>
                <li>Event payloads, deliveries and contract findings: 30 days.</li>
                <li>Alert log: 90 days.</li>
                <li>Account data, configuration, incidents, contracts and the audit log: while your account is open.</li>
                <li>After an account is closed: deleted within 30 days, and from nightly backups when they expire (they are kept for 14 days).</li>
              </ul>
            ),
          },
          {
            title: 'How we protect it',
            body: (
              <p>
                Traffic is encrypted with HTTPS; secrets are encrypted at rest with a separate key per organisation; passwords, API keys and sessions are
                stored only as hashes. Access is limited by role and recorded in the audit log. Details are on the{' '}
                <A to="/security">Security</A> page. If a breach affects your personal data, we will inform you and the Data Protection Board of India as the
                law requires.
              </p>
            ),
          },
          {
            title: 'Your rights',
            body: (
              <>
                <p>
                  You can ask to access, correct or delete your personal data, withdraw consent, and nominate someone to exercise these rights for you. Write
                  to {email}; we will reply within 30 days. Much of your data you can also view and change directly in the dashboard.
                </p>
                <p>
                  For personal data about your own customers inside your events, they should contact you (as the business they dealt with); we will help
                  you respond.
                </p>
                <p>If you are not satisfied with our answer, you can complain to the Data Protection Board of India.</p>
              </>
            ),
          },
          {
            title: 'Cookies and browser storage',
            body: (
              <p>
                The dashboard stores your sign-in session in your browser; without it you cannot stay signed in. We use no advertising or third-party
                tracking cookies.
              </p>
            ),
          },
          {
            title: 'Children',
            body: <p>Relaya is a service for businesses and is not meant for anyone under 18.</p>,
          },
          {
            title: 'Changes',
            body: (
              <p>
                We will post changes here with a new date, and tell you in the dashboard or by email before important changes apply.
              </p>
            ),
          },
          {
            title: 'Contact',
            body: <p>Privacy questions and requests: {email}</p>,
          },
        ]}
      />
    </PublicPage>
  )
}
