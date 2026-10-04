// A stand-in for the Slack Web API methods robin calls with its bot token when
// a scheduled message is added. The robin server under test is started with
// --slack-api-url pointing here.
//
// Endpoints (form-encoded POST, as slack-go sends them):
//   /api/auth.test              the bot user U0E2EROBIN
//   /api/conversations.info     the channels below; any other is channel_not_found
//   /api/conversations.members  the members of those channels, one per page
//   GET /__control              the recorded calls, as {"calls": [{"method", "channel"}]}
//   POST /__control             {"fail_next": "<method>"} makes the next call of
//                               that method fail with internal_error
//   DELETE /__control           clears the records and the failure
import { createServer } from 'node:http'

const port = Number(process.env.FAKE_SLACK_PORT ?? 18083)
const botUserID = 'U0E2EROBIN'
const e2eUserID = process.env.FAKE_SLACK_USER_ID ?? 'U0E2ETEST'

const channels = {
  C0E2EGENERAL: { name: 'general', archived: false, members: [botUserID, e2eUserID] },
  C0E2ENOROBIN: { name: 'no-robin', archived: false, members: [e2eUserID] },
  C0E2ENOTMINE: { name: 'not-mine', archived: false, members: [botUserID] },
  C0E2EARCHIVED: { name: 'archived', archived: true, members: [botUserID, e2eUserID] },
}

let calls = []
let failNext = ''

function json(res, status, body) {
  res.writeHead(status, { 'Content-Type': 'application/json' })
  res.end(JSON.stringify(body))
}

async function readForm(req) {
  let raw = ''
  for await (const chunk of req) {
    raw += chunk
  }
  return new URLSearchParams(raw)
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${port}`)

  if (url.pathname === '/__control') {
    if (req.method === 'DELETE') {
      calls = []
      failNext = ''
      json(res, 200, { ok: true })
      return
    }
    if (req.method === 'POST') {
      let raw = ''
      for await (const chunk of req) {
        raw += chunk
      }
      failNext = JSON.parse(raw || '{}').fail_next ?? ''
      json(res, 200, { ok: true })
      return
    }
    json(res, 200, { calls })
    return
  }

  if (req.method !== 'POST' || !url.pathname.startsWith('/api/')) {
    json(res, 404, { ok: false, error: 'unknown_method' })
    return
  }
  const method = url.pathname.slice('/api/'.length)
  const form = await readForm(req)
  const channelID = form.get('channel') ?? ''
  calls.push({ method, channel: channelID })

  if (failNext === method) {
    failNext = ''
    json(res, 200, { ok: false, error: 'internal_error' })
    return
  }

  switch (method) {
    case 'auth.test':
      json(res, 200, { ok: true, user_id: botUserID, team_id: 'T0E2ETEST', user: 'robin' })
      return
    case 'conversations.info': {
      const ch = channels[channelID]
      if (!ch) {
        json(res, 200, { ok: false, error: 'channel_not_found' })
        return
      }
      json(res, 200, {
        ok: true,
        channel: { id: channelID, name: ch.name, is_channel: true, is_private: false, is_archived: ch.archived },
      })
      return
    }
    case 'conversations.members': {
      const ch = channels[channelID]
      if (!ch) {
        json(res, 200, { ok: false, error: 'channel_not_found' })
        return
      }
      // One member per page, so robin has to follow the cursor.
      const start = Number(form.get('cursor') || 0)
      const page = ch.members.slice(start, start + 1)
      const next = start + 1 < ch.members.length ? String(start + 1) : ''
      json(res, 200, { ok: true, members: page, response_metadata: { next_cursor: next } })
      return
    }
    default:
      json(res, 200, { ok: false, error: 'unknown_method' })
  }
})

server.listen(port, '127.0.0.1')
