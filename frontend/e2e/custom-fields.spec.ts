import { test, expect, type Page, type Route } from '@playwright/test'
import { mockAdminApi } from './mock'

// Custom Fields A: the Custom Fields admin page in a real browser. Reordering
// by keyboard and pointer, the phone layout, and the two pages that edit the
// same places staying in step. Every call is faked here (no Go server); the
// shapes are the ones the design gives for the new admin calls.

type Def = {
  id: string; name: string; field_type: string; options?: string[]; retired_options?: string[]
  sort_order: number; active: boolean; created_at: string
}
type Place = { id: string; fieldId: string; catId: string; typeId?: string; show: boolean; required: boolean }

const D = '2026-09-20T10:05:00Z'
const CATS = [{ id: 'cat-1', name: 'Hardware' }, { id: 'cat-2', name: 'Network' }]
const TYPES: Record<string, { id: string; name: string }[]> = {
  'cat-1': [{ id: 'ty-1', name: 'Laptop' }],
  'cat-2': [{ id: 'ty-2', name: 'VPN' }],
}
const node = (n: { id: string; name: string }) => ({ ...n, active: true })

function freshDefs(): Def[] {
  return [
    { id: 'f-dept', name: 'Department', field_type: 'select', options: ['Finance', 'Sales, EMEA', 'IT'], retired_options: ['Marketing', 'Legal'], sort_order: 1, active: true, created_at: D },
    { id: 'f-phone', name: 'Phone number', field_type: 'text', sort_order: 2, active: true, created_at: D },
    { id: 'f-asset', name: 'Asset Tag', field_type: 'text', sort_order: 3, active: true, created_at: D },
    { id: 'f-old', name: 'Old cost centre', field_type: 'number', sort_order: 4, active: false, created_at: D },
  ]
}

const USAGE: Record<string, object> = {
  'f-dept': {
    tickets_with_value: 34,
    values: [
      { value: 'Finance', tickets: 17 }, { value: 'Sales, EMEA', tickets: 14 }, { value: 'IT', tickets: 0 },
      { value: 'Marketing', tickets: 3 }, { value: 'Legal', tickets: 0 },
    ],
  },
}

/** A stateful fake of the custom-field and category calls, with a record of the writes. */
async function mockFields(page: Page) {
  await mockAdminApi(page, { short: true })
  const state = {
    defs: freshDefs(),
    places: [
      { id: 'p-1', fieldId: 'f-dept', catId: 'cat-1', show: true, required: true },
      { id: 'p-2', fieldId: 'f-dept', catId: 'cat-2', typeId: 'ty-2', show: true, required: false },
      { id: 'p-3', fieldId: 'f-phone', catId: 'cat-1', typeId: 'ty-1', show: true, required: false },
    ] as Place[],
    puts: [] as string[][],
    patches: [] as Record<string, unknown>[],
  }
  let nextPlace = 10

  const placement = (p: Place) => ({
    id: p.id, field_def_id: p.fieldId, scope_type: p.typeId ? 'type' : 'category', scope_id: p.typeId ?? p.catId, sort_order: 0,
    visible_on_new: p.show, required_on_new: p.required,
    category: node(CATS.find((c) => c.id === p.catId)!),
    type: p.typeId ? node(TYPES[p.catId].find((t) => t.id === p.typeId)!) : null,
    item: null,
  })

  async function handle(route: Route) {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1/admin', '')
    const method = req.method()
    const body = () => req.postDataJSON() as Record<string, unknown>
    let m: RegExpMatchArray | null

    // place_count is on the list only, as on the real API.
    if (path === '/custom-fields' && method === 'GET') {
      return route.fulfill({ json: state.defs.map((d) => ({ ...d, place_count: state.places.filter((p) => p.fieldId === d.id).length })) })
    }
    if (path === '/custom-fields' && method === 'POST') {
      const b = body()
      const def: Def = { id: 'f-new', name: b.name as string, field_type: b.field_type as string, options: b.options as string[] | undefined, sort_order: b.sort_order as number, active: true, created_at: D }
      state.defs.push(def)
      return route.fulfill({ status: 201, json: def })
    }
    if (path === '/custom-fields/order' && method === 'PUT') {
      const ids = (body().field_def_ids as string[])
      state.puts.push(ids)
      state.defs = ids.map((id, n) => ({ ...state.defs.find((d) => d.id === id)!, sort_order: n + 1 }))
      return route.fulfill({ json: state.defs.map((d) => ({ ...d, place_count: state.places.filter((p) => p.fieldId === d.id).length })) })
    }
    if ((m = path.match(/^\/custom-fields\/([^/]+)\/assignments$/)) && method === 'GET') {
      return route.fulfill({ json: state.places.filter((p) => p.fieldId === m![1]).map(placement) })
    }
    if ((m = path.match(/^\/custom-fields\/([^/]+)\/usage$/)) && method === 'GET') {
      return route.fulfill({ json: USAGE[m[1]] ?? { tickets_with_value: 0, values: [] } })
    }
    if ((m = path.match(/^\/custom-fields\/([^/]+)$/)) && method === 'PATCH') {
      const b = body()
      state.patches.push(b)
      state.defs = state.defs.map((d) => (d.id === m![1] ? { ...d, ...b } : d))
      return route.fulfill({ json: state.defs.find((d) => d.id === m![1]) })
    }

    if (path === '/categories' && method === 'GET') return route.fulfill({ json: CATS.map((c, i) => ({ ...node(c), sort_order: i + 1 })) })
    if ((m = path.match(/^\/categories\/([^/]+)\/types$/)) && method === 'GET') {
      return route.fulfill({ json: (TYPES[m[1]] ?? []).map((t) => ({ ...node(t), category_id: m![1], sort_order: 1 })) })
    }
    if (path.match(/^\/categories\/[^/]+\/types\/[^/]+\/items$/) && method === 'GET') return route.fulfill({ json: [] })

    // Categories page: its Fields subsection reads and writes the same places.
    if ((m = path.match(/^\/categories\/([^/]+)\/fields$/))) {
      if (method === 'GET') {
        const rows = state.places.filter((p) => p.catId === m![1] && !p.typeId).map((p) => ({
          ...placement(p), field_def: state.defs.find((d) => d.id === p.fieldId),
        }))
        return route.fulfill({ json: rows })
      }
      if (method === 'POST') {
        const b = body()
        const p: Place = { id: `p-${nextPlace++}`, fieldId: b.field_def_id as string, catId: m[1], show: b.visible_on_new as boolean, required: b.required_on_new as boolean }
        state.places.push(p)
        return route.fulfill({ status: 201, json: placement(p) })
      }
    }
    if ((m = path.match(/^\/categories\/([^/]+)\/fields\/([^/]+)$/)) && method === 'DELETE') {
      state.places = state.places.filter((p) => p.id !== m![2])
      return route.fulfill({ status: 204, body: '' })
    }
    return route.fallback()
  }
  await page.route('**/api/v1/admin/**', handle)
  return state
}

const handleOf = (page: Page, name: string) => page.getByRole('button', { name: `Reorder ${name}` })
const rowToggle = (page: Page, name: string) => page.getByRole('button', { expanded: false }).filter({ hasText: new RegExp(`^${name}`) })
// dnd-kit renders one live region per DndContext; the one that is speaking has text.
const liveRegion = (page: Page) => page.locator('[id^="DndLiveRegion"]:not(:empty)')

// dnd-kit starts listening for the arrow keys one tick after it announces the
// pick-up, so a key pressed the instant the announcement shows is lost. A person
// is never that fast.
const pickedUp = (page: Page) => page.waitForTimeout(300)

// Presses ArrowDown and waits for the announcement of the move. On a loaded
// machine the first press can still beat dnd-kit's listener, so it is pressed
// again only while nothing has been announced.
async function arrowDownUntil(page: Page, announced: string) {
  await expect(async () => {
    const said = (await liveRegion(page).textContent()) ?? ''
    if (!said.includes(announced)) await page.keyboard.press('ArrowDown')
    await expect(liveRegion(page)).toContainText(announced, { timeout: 1500 })
  }).toPass({ timeout: 15_000 })
}

test.describe('reordering fields', () => {
  test('keyboard: Space, ArrowDown, Space sends the new order, announces it and keeps focus', async ({ page }) => {
    const state = await mockFields(page)
    await page.goto('/admin/custom-fields')
    const handle = handleOf(page, 'Department')
    await expect(handle).toBeVisible()
    await page.waitForLoadState('networkidle')

    await handle.focus()
    await page.keyboard.press('Space')
    await expect(liveRegion(page)).toContainText('Picked up Department')
    await pickedUp(page)
    await arrowDownUntil(page, 'Department is now over Phone number, position 2 of 3')
    await page.keyboard.press('Space')

    // The inactive "Old cost centre" is hidden but still in the list that is sent.
    await expect.poll(() => state.puts).toEqual([['f-phone', 'f-dept', 'f-asset', 'f-old']])
    await expect(liveRegion(page)).toContainText('Dropped Department, position 2 of 3')
    await expect(handle).toBeFocused()
    const names = await page.getByRole('button', { name: /^Reorder / }).evaluateAll((els) => els.map((e) => e.getAttribute('aria-label')))
    expect(names).toEqual(['Reorder Phone number', 'Reorder Department', 'Reorder Asset Tag'])
  })

  test('Escape cancels a reorder and sends nothing', async ({ page }) => {
    const state = await mockFields(page)
    await page.goto('/admin/custom-fields')
    const handle = handleOf(page, 'Department')
    await handle.focus()
    await page.keyboard.press('Space')
    await expect(liveRegion(page)).toContainText('Picked up Department')
    await arrowDownUntil(page, 'is now over Phone number')
    await page.keyboard.press('Escape')

    await expect(liveRegion(page)).toContainText('Reorder cancelled')
    expect(state.puts).toEqual([])
    const names = await page.getByRole('button', { name: /^Reorder / }).evaluateAll((els) => els.map((e) => e.getAttribute('aria-label')))
    expect(names).toEqual(['Reorder Department', 'Reorder Phone number', 'Reorder Asset Tag'])
  })

  test('a pointer drag sends the same order', async ({ page }) => {
    const state = await mockFields(page)
    await page.goto('/admin/custom-fields')
    const from = await handleOf(page, 'Department').boundingBox()
    const to = await handleOf(page, 'Phone number').boundingBox()
    expect(from && to).toBeTruthy()

    await page.mouse.move(from!.x + from!.width / 2, from!.y + from!.height / 2)
    await page.mouse.down()
    // In steps, so the sensor sees a drag and not a jump.
    await page.mouse.move(from!.x + from!.width / 2, from!.y + from!.height / 2 + 12, { steps: 4 })
    await page.mouse.move(to!.x + to!.width / 2, to!.y + to!.height / 2 + 12, { steps: 12 })
    await page.mouse.up()

    await expect.poll(() => state.puts).toEqual([['f-phone', 'f-dept', 'f-asset', 'f-old']])
  })
})

test.describe('reordering options', () => {
  test('keyboard reorder, then Save, sends the options in the new order', async ({ page }) => {
    const state = await mockFields(page)
    await page.goto('/admin/custom-fields')
    await rowToggle(page, 'Department').click()
    await expect(page.getByText('Fixed: 34 tickets have a value for this field.')).toBeVisible()

    const handle = handleOf(page, 'Finance')
    await handle.focus()
    await page.keyboard.press('Space')
    await expect(liveRegion(page)).toContainText('Picked up Finance')
    await pickedUp(page)
    await arrowDownUntil(page, 'is now over Sales, EMEA')
    await page.keyboard.press('Space')
    await expect(liveRegion(page)).toContainText('Dropped Finance, position 2 of 3')
    await expect(handle).toBeFocused()

    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => state.patches).toEqual([{ options: ['Sales, EMEA', 'Finance', 'IT'] }])
  })
})

test.describe('creating a field', () => {
  test('shows the banner, opens Where it appears and focuses Add a place', async ({ page }) => {
    await mockFields(page)
    await page.goto('/admin/custom-fields')
    await page.getByRole('button', { name: /New field/ }).click()
    await page.getByLabel('Name').fill('Serial number')
    await page.getByRole('button', { name: 'Create field' }).click()

    await expect(page.getByText("Serial number created. It isn't on any ticket form yet. Choose where it appears.")).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Where it appears' })).toBeVisible()
    await expect(page.getByText('Not on any form yet.')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Add a place' })).toBeFocused()
  })
})

test.describe('the Categories page and this one edit the same places', () => {
  test('a place added here shows there, and removing it there removes it here', async ({ page }) => {
    await mockFields(page)
    await page.goto('/admin/custom-fields')
    await rowToggle(page, 'Asset Tag').click()
    const form = page.getByRole('group', { name: 'Add a place' })
    await form.getByLabel('Category').selectOption('cat-1')
    await form.getByRole('button', { name: 'Add' }).click()
    await expect(page.getByRole('button', { name: 'Remove Hardware' })).toBeVisible()

    await page.goto('/admin/categories')
    await page.getByRole('button', { name: 'Expand' }).first().click()
    await expect(page.getByRole('link', { name: 'Asset Tag' })).toBeVisible()
    await page.getByTitle('Remove assignment').last().click()
    await expect(page.getByRole('link', { name: 'Asset Tag' })).toHaveCount(0)

    await page.goto('/admin/custom-fields')
    await rowToggle(page, 'Asset Tag').click()
    await expect(page.getByText('Not on any form yet.')).toBeVisible()
  })

  test('a link from the Categories page opens that field', async ({ page }) => {
    await mockFields(page)
    await page.goto('/admin/categories')
    await page.getByRole('button', { name: 'Expand' }).first().click()
    await page.getByRole('link', { name: 'Department' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Custom Fields' })).toBeVisible()
    await expect(page.getByRole('group', { name: 'Department' }).getByLabel('Name')).toHaveValue('Department')
  })
})

// A phone: nothing scrolls sideways, and a finger can hit everything. Checks
// run in four states, as the design lists: the list, an expanded select field
// with its retired options out, Add a place, and Show inactive.
for (const width of [320, 375, 390]) {
  test.describe(`phone ${width}px`, () => {
    test.use({ hasTouch: true, isMobile: true })

    async function check(page: Page) {
      await page.waitForLoadState('networkidle')
      const result = await page.evaluate((w) => {
        const main = document.querySelector('main') as HTMLElement
        const small: string[] = []
        const controls = main.querySelectorAll('button, select, a[href], input:not([type=checkbox]), label:has(input[type=checkbox])')
        for (const el of controls) {
          const r = el.getBoundingClientRect()
          if (r.width === 0 || r.height === 0) continue
          if (r.width < 43.5 || r.height < 43.5) {
            small.push(`<${el.tagName.toLowerCase()}> ${Math.round(r.width)}x${Math.round(r.height)} "${(el.getAttribute('aria-label') ?? el.textContent ?? '').trim().slice(0, 30)}"`)
          }
        }
        return { main: main.scrollWidth, box: main.clientWidth, doc: document.documentElement.scrollWidth, view: w, small }
      }, width)
      expect(result.main, 'main scrolls sideways').toBeLessThanOrEqual(result.box)
      expect(result.doc, 'the page scrolls sideways').toBeLessThanOrEqual(result.view)
      expect(result.small, 'controls under 44x44').toEqual([])
    }

    async function open(page: Page) {
      await mockFields(page)
      await page.setViewportSize({ width, height: 844 })
      await page.goto('/admin/custom-fields')
      await expect(page.getByRole('button', { name: /^Reorder Department/ })).toBeVisible()
    }

    test('the list', async ({ page }) => {
      await open(page)
      await check(page)
    })

    test('an expanded select field with retired options shown', async ({ page }) => {
      await open(page)
      await rowToggle(page, 'Department').click()
      await page.getByRole('checkbox', { name: 'Show retired' }).check()
      await expect(page.getByRole('button', { name: 'Delete IT' })).toBeVisible()
      await expect(page.getByRole('button', { name: 'Retire Finance' })).toBeVisible()
      await expect(page.getByRole('button', { name: 'Bring back Marketing' })).toBeVisible()
      await check(page)
    })

    test('Add a place', async ({ page }) => {
      await open(page)
      await rowToggle(page, 'Asset Tag').click()
      await page.getByRole('button', { name: 'Add a place' }).click()
      const form = page.getByRole('group', { name: 'Add a place' })
      await form.getByLabel('Category').selectOption('cat-2')
      await expect(form.getByLabel('Type')).toBeVisible()
      await check(page)
    })

    test('Show inactive', async ({ page }) => {
      await open(page)
      await page.getByRole('checkbox', { name: 'Show inactive' }).check()
      await expect(page.getByRole('button', { name: 'Reactivate Old cost centre' })).toBeVisible()
      await check(page)
    })
  })
}
