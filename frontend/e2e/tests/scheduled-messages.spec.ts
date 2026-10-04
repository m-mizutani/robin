import { expect, test, type Page } from '@playwright/test'
import { e2eUserID, fakeSlackURL } from '../../playwright.e2e.config'

// Scheduled messages on the settings page, against the real server. The bot
// calls e2e/fake-slack.mjs, which knows these channels:
//   C0E2EGENERAL   Robin and the user are members
//   C0E2ENOROBIN   only the user is a member
//   C0E2ENOTMINE   only Robin is a member
//   C0E2EARCHIVED  archived
// Running the jobs is the schedule command's work, which needs Firestore; it
// is covered by the Go tests.

const jobsAPI = '/api/v1/jobs'

async function signIn(page: Page) {
  await page.goto('/login')
  await page.getByRole('button', { name: 'Sign in with Slack' }).click()
  await expect(page).toHaveURL('/settings')
  await expect(page.getByText(`Signed in as ${e2eUserID}`)).toBeVisible()
}

// The tests share one server; each starts without scheduled messages.
async function deleteAllJobs(page: Page) {
  const res = await page.request.get(jobsAPI)
  expect(res.status()).toBe(200)
  const body = (await res.json()) as { jobs: { id: string }[] }
  for (const job of body.jobs) {
    expect((await page.request.delete(`${jobsAPI}/${job.id}`)).status()).toBe(200)
  }
}

async function addJob(page: Page, channelID: string, time = '09:00', zone = 'Asia/Tokyo') {
  await page.getByLabel('Channel ID').fill(channelID)
  await page.getByLabel('Time', { exact: true }).fill(time)
  await page.getByLabel('Time zone').selectOption(zone)
  await page.getByRole('button', { name: 'Add', exact: true }).click()
}

const section = (page: Page) => page.getByRole('region', { name: 'Scheduled messages' })

test.beforeEach(async ({ page, request }) => {
  await signIn(page)
  await deleteAllJobs(page)
  await request.delete(`${fakeSlackURL}/__control`)
  await page.reload()
})

test('a user without scheduled messages sees the form above the integrations', async ({ page }) => {
  await expect(section(page).getByText('No scheduled messages yet.')).toBeVisible()
  await expect(page.getByRole('heading', { level: 2 })).toHaveText(['Scheduled messages', 'Integrations'])
  await expect(page.getByLabel('Channel ID')).toHaveValue('')
  await expect(page.getByLabel('Time', { exact: true })).toHaveValue('09:00')
})

test('adding a channel lists the morning greeting, and it stays after a reload', async ({ page, request }) => {
  await addJob(page, 'C0E2EGENERAL', '09:00', 'Asia/Tokyo')

  const row = page.getByRole('listitem', { name: 'Morning greeting in #general' })
  await expect(row).toBeVisible()
  await expect(row.getByText('Every day at 09:00 (Asia/Tokyo)')).toBeVisible()
  await expect(row.getByText(/^Next post: [A-Z][a-z]{2} \d{1,2}, 09:00$/)).toBeVisible()
  await expect(row.getByText('Last run: not run yet')).toBeVisible()
  await expect(page.getByLabel('Channel ID')).toHaveValue('')
  await expect(section(page).getByText('No scheduled messages yet.')).toHaveCount(0)

  // The server checked the channel with Slack before saving it, following
  // the member list across pages.
  const control = (await (await request.get(`${fakeSlackURL}/__control`)).json()) as {
    calls: { method: string; channel: string }[]
  }
  expect(control.calls.map((c) => c.method)).toEqual([
    'conversations.info',
    'auth.test',
    'conversations.members',
    'conversations.members',
  ])

  await page.reload()
  await expect(page.getByRole('listitem', { name: 'Morning greeting in #general' })).toBeVisible()
})

for (const { channel, message } of [
  {
    channel: 'C0E2ENOROBIN',
    message: 'Robin is not a member of this channel. Run /invite @robin in the channel, then add it again.',
  },
  { channel: 'C0E2ENOTMINE', message: 'You can add only channels you are a member of.' },
  { channel: 'C0E2EARCHIVED', message: 'Channel C0E2EARCHIVED is archived. Choose a channel that is in use.' },
  {
    channel: 'C0E2EMISSING',
    message: 'Robin cannot find channel C0E2EMISSING. Check the ID. For a private channel, invite Robin to it first.',
  },
]) {
  test(`a channel that cannot be used (${channel}) is not added and the reason is shown`, async ({ page }) => {
    await addJob(page, channel)

    await expect(section(page).getByRole('alert')).toHaveText(message)
    await expect(page.getByLabel('Channel ID')).toHaveValue(channel)
    await expect(section(page).getByText('No scheduled messages yet.')).toBeVisible()

    const res = await page.request.get(jobsAPI)
    expect(((await res.json()) as { jobs: unknown[] }).jobs).toHaveLength(0)
  })
}

test('a malformed channel ID is stopped by the form, and the API rejects it too', async ({ page }) => {
  await addJob(page, 'general')
  const valid = await page.getByLabel('Channel ID').evaluate((el) => (el as HTMLInputElement).validity.valid)
  expect(valid).toBe(false)
  await expect(section(page).getByRole('alert')).toHaveCount(0)

  for (const data of [
    { kind: 'hello', channel_id: 'general', hour: 9, minute: 0, time_zone: 'Asia/Tokyo' },
    // "Local" is the zone of the server process, not a zone the user chose.
    { kind: 'hello', channel_id: 'C0E2EGENERAL', hour: 9, minute: 0, time_zone: 'Local' },
    { kind: 'hello', channel_id: 'C0E2EGENERAL', hour: 24, minute: 0, time_zone: 'UTC' },
    { kind: 'unknown', channel_id: 'C0E2EGENERAL', hour: 9, minute: 0, time_zone: 'UTC' },
  ]) {
    const res = await page.request.post(jobsAPI, { data })
    expect(res.status()).toBe(400)
    expect(await res.json()).toEqual({ error: 'invalid_input' })
  }
})

test('a failure to add is shown with the input kept, and adding again works', async ({ page, request }) => {
  await request.post(`${fakeSlackURL}/__control`, { data: { fail_next: 'conversations.info' } })
  await addJob(page, 'C0E2EGENERAL')

  await expect(section(page).getByRole('alert')).toHaveText('Could not add the scheduled message. Try again.')
  await expect(page.getByLabel('Channel ID')).toHaveValue('C0E2EGENERAL')
  await expect(section(page).getByText('No scheduled messages yet.')).toBeVisible()

  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByRole('listitem', { name: 'Morning greeting in #general' })).toBeVisible()
  await expect(section(page).getByRole('alert')).toHaveCount(0)
})

// The memory repository does not fail, so the next two tests cut the
// connection of the first request instead; the server is not mocked.
test('a list that could not be loaded can be loaded again', async ({ page }) => {
  await addJob(page, 'C0E2EGENERAL')
  await expect(page.getByRole('listitem', { name: 'Morning greeting in #general' })).toBeVisible()

  let cut = true
  await page.route('**/api/v1/jobs', (route) => {
    if (cut && route.request().method() === 'GET') {
      cut = false
      return route.abort('connectionfailed')
    }
    return route.fallback()
  })
  await page.reload()
  await expect(section(page).getByRole('alert')).toHaveText('Could not load your scheduled messages.')

  await section(page).getByRole('button', { name: 'Load again' }).click()
  await expect(page.getByRole('listitem', { name: 'Morning greeting in #general' })).toBeVisible()
})

test('a failed deletion keeps the message, and deleting again works', async ({ page }) => {
  await addJob(page, 'C0E2EGENERAL')
  const row = page.getByRole('listitem', { name: 'Morning greeting in #general' })
  await expect(row).toBeVisible()

  let cut = true
  await page.route('**/api/v1/jobs/*', (route) => {
    if (cut) {
      cut = false
      return route.abort('connectionfailed')
    }
    return route.fallback()
  })
  const deleteButton = row.getByRole('button', { name: 'Delete Morning greeting in #general' })
  await deleteButton.click()
  await expect(row.getByRole('alert')).toHaveText('Could not delete this scheduled message. Try again.')
  await expect(deleteButton).toBeEnabled()

  await deleteButton.click()
  await expect(row).toHaveCount(0)
  await page.reload()
  await expect(section(page).getByText('No scheduled messages yet.')).toBeVisible()
})

test('deleting a scheduled message removes it for good', async ({ page }) => {
  await addJob(page, 'C0E2EGENERAL')
  const row = page.getByRole('listitem', { name: 'Morning greeting in #general' })
  await expect(row).toBeVisible()

  await row.getByRole('button', { name: 'Delete Morning greeting in #general' }).click()
  await expect(row).toHaveCount(0)
  await expect(section(page).getByText('No scheduled messages yet.')).toBeVisible()

  await page.reload()
  await expect(section(page).getByText('No scheduled messages yet.')).toBeVisible()
})

test('the form is replaced once the user has the most scheduled messages', async ({ page }) => {
  const list = (await (await page.request.get(jobsAPI)).json()) as { max_jobs: number }
  for (let i = 0; i < list.max_jobs; i++) {
    const res = await page.request.post(jobsAPI, {
      data: { kind: 'hello', channel_id: 'C0E2EGENERAL', hour: 9, minute: i, time_zone: 'UTC' },
    })
    expect(res.status()).toBe(201)
  }
  await page.reload()

  await expect(
    section(page).getByText(`You have ${list.max_jobs} scheduled messages, the most allowed. Delete one to add another.`),
  ).toBeVisible()
  await expect(page.getByLabel('Channel ID')).toHaveCount(0)

  const res = await page.request.post(jobsAPI, {
    data: { kind: 'hello', channel_id: 'C0E2EGENERAL', hour: 10, minute: 0, time_zone: 'UTC' },
  })
  expect(res.status()).toBe(409)
  expect(await res.json()).toEqual({ error: 'job_limit_reached' })
})

test('signed out, the scheduled messages API answers 401', async ({ page }) => {
  await addJob(page, 'C0E2EGENERAL')
  await expect(page.getByRole('listitem', { name: 'Morning greeting in #general' })).toBeVisible()
  const { jobs } = (await (await page.request.get(jobsAPI)).json()) as { jobs: { id: string }[] }

  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page).toHaveURL('/login')

  expect((await page.request.get(jobsAPI)).status()).toBe(401)
  expect(
    (
      await page.request.post(jobsAPI, {
        data: { kind: 'hello', channel_id: 'C0E2EGENERAL', hour: 9, minute: 0, time_zone: 'UTC' },
      })
    ).status(),
  ).toBe(401)
  expect((await page.request.delete(`${jobsAPI}/${jobs[0].id}`)).status()).toBe(401)
})
