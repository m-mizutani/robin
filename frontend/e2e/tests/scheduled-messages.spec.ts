import { expect, test, type Page } from '@playwright/test'
import { e2eUserID } from '../../playwright.e2e.config'

// Scheduled messages on the settings page, against the real server. Running
// the jobs is the schedule command's work, which needs Firestore; it is
// covered by the Go tests.
//
// The tests share one server and one user, and a job setting cannot be
// deleted, so they run in order: the first test sees the user before any
// setting is saved.
test.describe.configure({ mode: 'serial' })

const jobsAPI = '/api/v1/jobs'

async function signIn(page: Page) {
  await page.goto('/login')
  await page.getByRole('button', { name: 'Sign in with Slack' }).click()
  await expect(page).toHaveURL('/settings')
  await expect(page.getByText(`Signed in as ${e2eUserID}`)).toBeVisible()
}

async function deleteAllTriggers(page: Page) {
  const res = await page.request.get(jobsAPI)
  expect(res.status()).toBe(200)
  const { triggers } = (await res.json()) as { triggers: { id: string }[] }
  for (const t of triggers) {
    expect((await page.request.delete(`${jobsAPI}/triggers/${t.id}`)).status()).toBe(200)
  }
}

const section = (page: Page) => page.getByRole('region', { name: 'Scheduled messages' })
const greeting = (page: Page) => page.getByRole('region', { name: 'Morning greeting' })

async function saveSetting(page: Page, channelID: string, zone: string) {
  await page.getByLabel('Channel ID').fill(channelID)
  await page.getByLabel('Time zone').selectOption(zone)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
}

async function addTime(page: Page, time: string) {
  await greeting(page).getByLabel('Time').fill(time)
  await greeting(page).getByRole('button', { name: 'Add', exact: true }).click()
}

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await deleteAllTriggers(page)
  await page.reload()
  // The section loads after the sign-in check; wait for it, so a route a
  // test adds next does not catch this load.
  await expect(greeting(page).getByText('No times yet.')).toBeVisible()
})

test('before the channel and time zone are saved, no time can be added', async ({ page }) => {
  await expect(page.getByRole('heading', { level: 2 })).toHaveText(['Scheduled messages', 'Integrations'])
  await expect(page.getByLabel('Channel ID')).toHaveValue('')
  await expect(greeting(page).getByText('No times yet.')).toBeVisible()
  await expect(greeting(page).getByText('Save the channel and time zone first.')).toBeVisible()
  await expect(greeting(page).getByRole('button', { name: 'Add', exact: true })).toBeDisabled()

  const res = await page.request.post(`${jobsAPI}/triggers`, { data: { job: 'hello', hour: 9, minute: 0 } })
  expect(res.status()).toBe(409)
  expect(await res.json()).toEqual({ error: 'setting_required' })
})

test('saving the channel and time zone keeps them and enables adding times', async ({ page }) => {
  await saveSetting(page, 'C0E2EGENERAL', 'Asia/Tokyo')
  await expect(section(page).getByRole('status')).toHaveText('Saved.')
  await expect(greeting(page).getByRole('button', { name: 'Add', exact: true })).toBeEnabled()

  // A further change is not saved yet, so "Saved." goes away.
  await page.getByLabel('Channel ID').fill('C0E2EOTHER')
  await expect(section(page).getByRole('status')).toHaveCount(0)

  await page.reload()
  await expect(page.getByLabel('Channel ID')).toHaveValue('C0E2EGENERAL')
  await expect(page.getByLabel('Time zone')).toHaveValue('Asia/Tokyo')
})

test('a malformed channel ID is stopped by the form, and the API rejects bad settings', async ({ page }) => {
  await page.getByLabel('Channel ID').fill('general')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  const valid = await page.getByLabel('Channel ID').evaluate((el) => (el as HTMLInputElement).validity.valid)
  expect(valid).toBe(false)

  for (const data of [
    { channel_id: 'general', time_zone: 'Asia/Tokyo' },
    // "Local" is the zone of the server process, not a zone the user chose.
    { channel_id: 'C0E2EGENERAL', time_zone: 'Local' },
    { channel_id: 'C0E2EGENERAL', time_zone: 'Mars/Base' },
  ]) {
    const res = await page.request.put(`${jobsAPI}/setting`, { data })
    expect(res.status()).toBe(400)
    expect(await res.json()).toEqual({ error: 'invalid_input' })
  }
})

test('times can be added and deleted, and they stay after a reload', async ({ page }) => {
  await saveSetting(page, 'C0E2EGENERAL', 'Asia/Tokyo')
  await expect(section(page).getByRole('status')).toHaveText('Saved.')

  await addTime(page, '18:30')
  await expect(greeting(page).getByRole('listitem', { name: '18:30' })).toBeVisible()
  await addTime(page, '09:00')
  await expect(greeting(page).getByRole('listitem')).toHaveCount(2)
  await expect(greeting(page).getByRole('listitem').first()).toHaveAccessibleName('09:00')
  await expect(greeting(page).getByText('No times yet.')).toHaveCount(0)

  await page.reload()
  await expect(greeting(page).getByRole('listitem')).toHaveCount(2)

  await greeting(page).getByRole('button', { name: 'Delete 18:30' }).click()
  await expect(greeting(page).getByRole('listitem', { name: '18:30' })).toHaveCount(0)
  await page.reload()
  await expect(greeting(page).getByRole('listitem')).toHaveCount(1)
  await expect(greeting(page).getByRole('listitem', { name: '09:00' })).toBeVisible()
})

test('the API rejects a bad time or an unknown job', async ({ page }) => {
  await saveSetting(page, 'C0E2EGENERAL', 'Asia/Tokyo')
  await expect(section(page).getByRole('status')).toHaveText('Saved.')
  for (const data of [
    { job: 'hello', hour: 24, minute: 0 },
    { job: 'hello', hour: 9, minute: 60 },
    { job: 'unknown', hour: 9, minute: 0 },
    // A missing hour is not midnight.
    { job: 'hello', minute: 0 },
  ]) {
    const res = await page.request.post(`${jobsAPI}/triggers`, { data })
    expect(res.status()).toBe(400)
    expect(await res.json()).toEqual({ error: 'invalid_input' })
  }
})

// The memory repository does not fail, so the next tests cut the connection
// of the first request instead; the server is not mocked.
async function cutFirst(page: Page, path: RegExp, method: string) {
  let cut = true
  await page.route((url) => path.test(url.pathname), (route) => {
    if (cut && route.request().method() === method) {
      cut = false
      return route.abort('connectionfailed')
    }
    return route.fallback()
  })
}

test('a list that could not be loaded can be loaded again', async ({ page }) => {
  await cutFirst(page, /^\/api\/v1\/jobs$/, 'GET')
  await page.reload()
  await expect(section(page).getByRole('alert')).toHaveText('Could not load your scheduled messages.')
  await section(page).getByRole('button', { name: 'Load again' }).click()
  await expect(page.getByLabel('Channel ID')).toBeVisible()
})

test('a failed save keeps the input, and saving again works', async ({ page }) => {
  await cutFirst(page, /^\/api\/v1\/jobs\/setting$/, 'PUT')
  await saveSetting(page, 'C0E2ERANDOM', 'UTC')
  await expect(section(page).getByRole('alert')).toHaveText('Could not save. Try again.')
  await expect(page.getByLabel('Channel ID')).toHaveValue('C0E2ERANDOM')

  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(section(page).getByRole('status')).toHaveText('Saved.')
  await page.reload()
  await expect(page.getByLabel('Channel ID')).toHaveValue('C0E2ERANDOM')
  await expect(page.getByLabel('Time zone')).toHaveValue('UTC')
})

test('a failed addition can be retried', async ({ page }) => {
  await cutFirst(page, /^\/api\/v1\/jobs\/triggers$/, 'POST')
  await addTime(page, '07:15')
  await expect(greeting(page).getByRole('alert')).toHaveText('Could not add the time. Try again.')
  await expect(greeting(page).getByText('No times yet.')).toBeVisible()

  await greeting(page).getByRole('button', { name: 'Add', exact: true }).click()
  await expect(greeting(page).getByRole('listitem', { name: '07:15' })).toBeVisible()
})

test('a failed deletion keeps the time, and deleting again works', async ({ page }) => {
  await addTime(page, '07:15')
  await expect(greeting(page).getByRole('listitem', { name: '07:15' })).toBeVisible()

  await cutFirst(page, /^\/api\/v1\/jobs\/triggers\/[^/]+$/, 'DELETE')
  const del = greeting(page).getByRole('button', { name: 'Delete 07:15' })
  await del.click()
  await expect(greeting(page).getByRole('alert')).toHaveText('Could not delete the time. Try again.')
  await expect(del).toBeEnabled()

  await del.click()
  await expect(greeting(page).getByRole('listitem', { name: '07:15' })).toHaveCount(0)
  await page.reload()
  await expect(greeting(page).getByText('No times yet.')).toBeVisible()
})

test('signed out, the scheduled messages API answers 401', async ({ page }) => {
  await addTime(page, '07:15')
  await expect(greeting(page).getByRole('listitem', { name: '07:15' })).toBeVisible()
  const { triggers } = (await (await page.request.get(jobsAPI)).json()) as { triggers: { id: string }[] }

  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page).toHaveURL('/login')

  expect((await page.request.get(jobsAPI)).status()).toBe(401)
  expect((await page.request.put(`${jobsAPI}/setting`, { data: { channel_id: 'C0E2EGENERAL', time_zone: 'UTC' } })).status()).toBe(401)
  expect((await page.request.post(`${jobsAPI}/triggers`, { data: { job: 'hello', hour: 9, minute: 0 } })).status()).toBe(401)
  expect((await page.request.delete(`${jobsAPI}/triggers/${triggers[0].id}`)).status()).toBe(401)
})
