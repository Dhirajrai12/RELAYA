/*!
 * Relaya Connect: open a Connect link in a popup and wait until the user has
 * connected their account.
 *
 *   <script src="https://YOUR-RELAYA-HOST/connect.js"></script>
 *   button.onclick = async () => {
 *     const link = await fetch('/your-backend/connect-link').then((r) => r.json())   // your backend creates it with your API key
 *     try {
 *       const { connectionId, endUserId } = await Relaya.connect(link.url)
 *     } catch (err) {
 *       // err.code: popup_blocked | closed | expired | failed | invalid_link | aborted
 *     }
 *   }
 *
 * Options: { signal: AbortSignal, onError(message), width, height }.
 * Call it from a click handler, or browsers block the popup.
 */
;(function (global) {
  'use strict'

  function ConnectError(code, message) {
    var e = new Error(message)
    e.name = 'RelayaConnectError'
    e.code = code
    return e
  }

  function parseLink(link) {
    var u = new URL(link, global.location.href)
    var m = u.pathname.match(/\/connect\/(cs_[A-Za-z0-9_-]+)\/?$/)
    if (!m) throw ConnectError('invalid_link', 'Not a Relaya Connect link: ' + link)
    return { url: u, origin: u.origin, token: m[1] }
  }

  function connect(link, opts) {
    opts = opts || {}
    return new Promise(function (resolve, reject) {
      var info
      try {
        info = parseLink(link)
      } catch (e) {
        return reject(e.code ? e : ConnectError('invalid_link', String(e && e.message)))
      }

      var page = new URL(info.url.href)
      page.searchParams.set('mode', 'popup')
      var w = opts.width || 520
      var h = opts.height || 720
      var left = Math.round((global.screenX || 0) + Math.max(0, ((global.outerWidth || w) - w) / 2))
      var top = Math.round((global.screenY || 0) + Math.max(0, ((global.outerHeight || h) - h) / 2))
      var popup = global.open(page.href, 'relaya-connect', 'popup=yes,width=' + w + ',height=' + h + ',left=' + left + ',top=' + top)
      if (!popup) {
        return reject(ConnectError('popup_blocked', 'The browser blocked the popup. Call Relaya.connect() from a click handler.'))
      }

      var statusURL = info.origin + '/api/v1/connect/sessions/' + encodeURIComponent(info.token)
      var done = false
      var closedSince = 0
      var lastError = ''
      var timer

      function closePopup() {
        try {
          popup.close()
        } catch (e) {
          /* the provider's pages may have cut the link to the popup */
        }
      }
      function finish(ok, value) {
        if (done) return
        done = true
        clearInterval(timer)
        if (opts.signal) opts.signal.removeEventListener('abort', onAbort)
        ;(ok ? resolve : reject)(value)
      }
      function onAbort() {
        closePopup()
        finish(false, ConnectError('aborted', 'Connecting was cancelled.'))
      }
      if (opts.signal) {
        if (opts.signal.aborted) return onAbort()
        opts.signal.addEventListener('abort', onAbort)
      }

      // Relaya is asked for the link's state: the provider's sign-in pages can
      // cut the popup off from this page, so messages between them don't work.
      function poll() {
        fetch(statusURL, { cache: 'no-store', credentials: 'omit' })
          .then(function (res) {
            if (res.status === 404) throw ConnectError('invalid_link', 'This Connect link isn’t valid.')
            return res.ok ? res.json() : null // 429/5xx: try again next time
          })
          .then(function (s) {
            if (!s || done) return
            if (s.status === 'completed') {
              closePopup()
              return finish(true, { status: 'connected', connectionId: s.connection_id, endUserId: s.end_user_id, provider: s.provider })
            }
            if (s.status === 'expired') {
              closePopup()
              return finish(false, ConnectError('expired', 'The link expired before the account was connected.'))
            }
            if (s.error && s.error !== lastError) {
              lastError = s.error
              if (opts.onError) opts.onError(s.error) // the user can still try again in the popup
            }
            var closed = false
            try {
              closed = popup.closed
            } catch (e) {
              closed = false
            }
            if (!closed) {
              closedSince = 0
              return
            }
            // Before the provider's sign-in, "closed" is reliable. After it, the
            // provider's pages can make an open popup look closed, so wait a
            // while longer unless the last attempt already failed.
            if (!s.started) return finish(false, ConnectError('closed', 'The window was closed before connecting.'))
            if (s.error) return finish(false, ConnectError('failed', s.error))
            closedSince = closedSince || Date.now()
            if (Date.now() - closedSince > (opts.closedGraceMs || 5 * 60 * 1000)) {
              finish(false, ConnectError('closed', 'The window was closed before connecting.'))
            }
          })
          .catch(function (e) {
            if (e && e.code) finish(false, e) // our own errors; network blips are retried
          })
      }
      timer = setInterval(poll, opts.intervalMs || 2000)
      poll()
    })
  }

  global.Relaya = global.Relaya || {}
  global.Relaya.connect = connect
})(typeof window !== 'undefined' ? window : this)
