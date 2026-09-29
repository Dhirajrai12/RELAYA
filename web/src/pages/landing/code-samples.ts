// Code shown in the landing page's self-typing demos.

export type Kind = 'p' | 'k' | 's' | 'c' | 'n' | 'f' | 'x'
export type Tok = [text: string, kind?: Kind]
export type Line = Tok[]

export const deliver: Line[] = [
  [['# A provider sends a webhook to your Relaya URL', 'c']],
  [['curl', 'f'], [' -X POST '], ['https://api.relaya.sbs/v1/in/in_7Kq2…', 's'], [' \\']],
  [['  -H '], ['"X-Razorpay-Signature: 3f9c1e…"', 's'], [' \\']],
  [['  -H '], ['"X-Razorpay-Event-Id: evt_Nw81"', 's'], [' \\']],
  [['  -d '], ["'{\"event\":\"payment.captured\",\"amount\":49900}'", 's']],
]

export const events: Line[] = [
  [['// Every delivery that failed verification today', 'c']],
  [['const', 'x'], [' res = '], ['await', 'x'], [' '], ['fetch', 'f'], ['('], ['`https://api.relaya.sbs/v1/orgs/${ORG}/events?status=rejected`', 's'], [', {']],
  [['  headers', 'k'], [': { '], ['Authorization', 'k'], [': '], ['`Bearer ${process.env.RELAYA_KEY}`', 's'], [' },']],
  [['})']],
  [['const', 'x'], [' { data } = '], ['await', 'x'], [' res.'], ['json', 'f'], ['()']],
  [['console', 'k'], ['.'], ['table', 'f'], ['(data.'], ['map', 'f'], ['(e => [e.type, e.signature, e.received_at]))']],
]

export const sendExample: Line[] = [
  [['# Any provider, any language: it is just an HTTPS POST', 'c']],
  [['curl', 'f'], [' -X POST '], ['https://api.relaya.sbs/v1/in/in_7Kq…', 's'], [' \\']],
  [['  -H '], ["'X-Razorpay-Signature: 3f9c…'", 's'], [' \\']],
  [['  -d '], ["'{\"event\":\"payment.captured\"}'", 's']],
]

export const rejectedExample: Line[] = [
  [['# Find every delivery that failed verification', 'c']],
  [['curl', 'f'], [' '], ['"https://api.relaya.sbs/v1/orgs/$ORG/events?status=rejected"', 's'], [' \\']],
  [['  -H '], ['"Authorization: Bearer rk_…"', 's']],
]
