import { expect, test, type Page } from '@playwright/test'

type Json = Record<string, unknown>

async function mockControlPlane(
  page: Page,
  options?: {
    sessions?: Json[]
    runs?: Record<string, Json[]>
    events?: Record<string, Json[]>
    run?: Record<string, Json>
    stm?: Record<string, Json>
  },
) {
  const sessions = options?.sessions ?? [
    { id: 's1', status: 'open', created_at: '2026-09-01T00:00:00Z' },
    { id: 's2', status: 'open', created_at: '2026-08-01T00:00:00Z' },
  ]
  const runs = options?.runs ?? {
    s1: [
      {
        id: 'r1',
        session_id: 's1',
        status: 'waiting_approval',
        created_at: '2026-09-01T00:01:00Z',
      },
    ],
    s2: [],
  }
  const events = options?.events ?? {
    s1: [
      {
        run_id: 'r1',
        seq: 1,
        type: 'message.user',
        payload: { text: 'review skill' },
      },
      {
        run_id: 'r1',
        seq: 2,
        type: 'tool.requested',
        payload: {
          id: 'call-1',
          name: 'fs.write',
          phase: 'review',
          model: 'pro',
          args: { path: 'memory/skills/review.md', content: '只记录可复用规则。' },
        },
      },
      {
        run_id: 'r1',
        seq: 3,
        type: 'memory.retrieved',
        payload: { hits: [{ run_id: 'old-run', seq: 8, kind: 'message.completed' }] },
      },
    ],
    s2: [],
  }
  const runByID = options?.run ?? {
    r1: { id: 'r1', session_id: 's1', status: 'waiting_approval', model: 'pro' },
  }
  const stmByID = options?.stm ?? {
    r1: { messages: [{ role: 'user', content: 'review skill' }] },
  }

  let decision: unknown
  let posted: unknown

  await page.route('**/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    const method = request.method()

    if (path === '/v1/workspace/open' && method === 'POST') {
      return route.fulfill({ json: { ok: true } })
    }

    if (path === '/v1/sessions' && method === 'GET') {
      return route.fulfill({ json: sessions })
    }
    if (path === '/v1/sessions' && method === 'DELETE') {
      sessions.splice(0, sessions.length)
      return route.fulfill({ json: { ok: true } })
    }
    if (path === '/v1/sessions' && method === 'POST') {
      const created = {
        id: 's-new',
        status: 'open',
        created_at: '2026-09-01T01:00:00Z',
      }
      sessions.unshift(created)
      runs['s-new'] = []
      events['s-new'] = []
      return route.fulfill({ json: created })
    }

    const sessionOnly = path.match(/^\/v1\/sessions\/([^/]+)$/)
    if (sessionOnly && method === 'DELETE') {
      const sessionID = decodeURIComponent(sessionOnly[1])
      const index = sessions.findIndex((item) => item.id === sessionID)
      if (index >= 0) sessions.splice(index, 1)
      delete runs[sessionID]
      delete events[sessionID]
      return route.fulfill({ json: { ok: true } })
    }

    const sessionMatch = path.match(
      /^\/v1\/sessions\/([^/]+)\/(runs|events|messages)$/,
    )
    if (sessionMatch) {
      const sessionID = decodeURIComponent(sessionMatch[1])
      const leaf = sessionMatch[2]
      if (leaf === 'runs') {
        return route.fulfill({ json: runs[sessionID] ?? [] })
      }
      if (leaf === 'events') {
        return route.fulfill({ json: events[sessionID] ?? [] })
      }
      if (leaf === 'messages' && method === 'POST') {
        posted = request.postDataJSON()
        const runID = 'r-new'
        const text =
          posted && typeof posted === 'object' && 'text' in posted
            ? String((posted as { text: unknown }).text)
            : ''
        runs[sessionID] = [
          {
            id: runID,
            session_id: sessionID,
            status: 'running',
            created_at: '2026-09-01T01:01:00Z',
          },
        ]
        events[sessionID] = [
          ...(events[sessionID] ?? []),
          { run_id: runID, seq: 1, type: 'message.user', payload: { text } },
          {
            run_id: runID,
            seq: 2,
            type: 'message.delta',
            payload: { text: 'ack ' },
          },
          {
            run_id: runID,
            seq: 3,
            type: 'message.completed',
            payload: { text: 'ack hello' },
          },
        ]
        runByID[runID] = {
          id: runID,
          session_id: sessionID,
          status: 'completed',
        }
        return route.fulfill({ json: { run_id: runID } })
      }
    }

    if (path === '/v1/runs/old-run/events/8') {
      return route.fulfill({
        json: {
          run_id: 'old-run',
          seq: 8,
          type: 'message.completed',
          payload: { text: 'source fact' },
        },
      })
    }

    const runMatch = path.match(/^\/v1\/runs\/([^/]+)(?:\/(.*))?$/)
    if (runMatch) {
      const runID = decodeURIComponent(runMatch[1])
      const rest = runMatch[2] ?? ''
      if (rest === '' && method === 'GET') {
        return route.fulfill({
          json: runByID[runID] ?? { id: runID, status: 'unknown' },
        })
      }
      if (rest === '' && method === 'DELETE') {
        return route.fulfill({ json: { ok: true } })
      }
      if (rest === 'stm') {
        return route.fulfill({ json: stmByID[runID] ?? { messages: [] } })
      }
      if (rest === 'decisions' && method === 'POST') {
        decision = request.postDataJSON()
        return route.fulfill({ json: { ok: true } })
      }
      if (rest === 'events' && method === 'GET') {
        return route.fulfill({
          status: 200,
          contentType: 'text/event-stream',
          body: ': connected\n\n',
        })
      }
    }

    return route.fulfill({ status: 404, json: { error: 'not_found' } })
  })

  return {
    decision: () => decision,
    posted: () => posted,
  }
}

test('timeline approval and provenance use the HTTP contract', async ({ page }) => {
  const mock = await mockControlPlane(page)
  await page.goto('/s/s1/r/r1')

  await expect(page.getByText('Waiting for approval').first()).toBeVisible()
  await expect(
    page.getByText('memory/skills/review.md', { exact: true }).first(),
  ).toBeVisible()
  await expect(
    page.getByText('只记录可复用规则。', { exact: true }).first(),
  ).toBeVisible()

  await page.getByRole('tab', { name: '审计' }).click()
  await expect(page.getByRole('heading', { name: 'Event Timeline' })).toBeVisible()
  await expect(
    page.getByRole('button', { name: 'tool.requested #2 fs.write' }),
  ).toBeVisible()

  await page.getByRole('button', { name: 'Allow' }).first().click()
  await expect.poll(() => mock.decision()).toEqual({ seq: 2, allow: true })

  await page.getByRole('tab', { name: 'STM' }).click()
  await expect(page.getByRole('tabpanel', { name: 'STM' })).toContainText('review skill')

  await page.getByRole('tab', { name: 'Memory' }).click()
  await expect(page.getByText('PROVENANCE')).toBeVisible()
  await page.getByRole('button', { name: /message.completed/ }).click()
  await expect(page.getByText('source fact')).toBeVisible()
})

test('chat send projects bubbles from the same events', async ({ page }) => {
  const mock = await mockControlPlane(page, {
    sessions: [],
    runs: {},
    events: {},
    run: {},
    stm: {},
  })
  await page.goto('/')

  await page.getByRole('textbox', { name: '发送消息' }).fill('hello')
  await page.getByRole('button', { name: '发送' }).click()
  await expect.poll(() => mock.posted()).toEqual({ text: 'hello' })
  await expect(page).toHaveURL(/\/s\/s-new\/r\/r-new/)
  await expect(page.getByText('hello').first()).toBeVisible()
  await expect(page.getByText('ack hello')).toBeVisible()
})

test('session switch and deep link stay on the control-plane URL', async ({ page }) => {
  await mockControlPlane(page)
  await page.goto('/s/s1/r/r1')
  await expect(page.getByText('review skill').first()).toBeVisible()
  await expect(page.getByRole('button', { name: '发送' })).toBeDisabled()

  await page.getByRole('button', { name: 'Session s2' }).click()
  await expect(page).toHaveURL(/\/s\/s2/)

  await page.goto('/s/s1/r/r1')
  await expect(page).toHaveURL(/\/s\/s1\/r\/r1/)
  await expect(page.getByText('Waiting for approval').first()).toBeVisible()
})

test('mobile switches among the three control-plane panes', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockControlPlane(page)
  await page.goto('/s/s1/r/r1')
  await page.getByRole('button', { name: 'Inspector' }).click()
  await expect(page.getByRole('heading', { name: 'Inspector' })).toBeVisible()
  await page.getByRole('button', { name: '对话' }).click()
  await expect(page.getByRole('textbox', { name: '发送消息' })).toBeVisible()
  await page.getByRole('tab', { name: '审计' }).click()
  await expect(page.getByRole('heading', { name: 'Event Timeline' })).toBeVisible()
  await page.getByRole('button', { name: 'Session', exact: true }).click()
  await expect(page.getByRole('heading', { name: '打开文件夹' })).toBeVisible()
})
