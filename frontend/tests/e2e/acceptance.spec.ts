import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installApiMock, type ApiMock } from './api-mock'

const viewportWidths = [320, 375, 768, 1024, 1440]

async function openView(page: Page, label: string) {
  if ((await page.viewportSize())!.width <= 760) {
    await page.getByRole('button', { name: '打开菜单' }).click()
    await expect(page.locator('.sidebar')).toHaveClass(/mobile-open/)
  }
  await page.getByRole('button', { name: label, exact: true }).click()
}

async function expectNoHorizontalOverflow(page: Page) {
  const sizes = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }))
  expect(sizes.content, `页面宽度 ${sizes.content}px 超出视口 ${sizes.viewport}px`).toBeLessThanOrEqual(sizes.viewport + 1)
}

async function saveEvidence(page: Page, testInfo: TestInfo, name: string) {
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`), animations: 'disabled' })
}

function assertClean(mock: ApiMock) {
  expect(mock.unmatched).toEqual([])
  expect(mock.consoleErrors).toEqual([])
}

test('顶栏主题切换会即时应用并持久化三套主题', async ({ page }, testInfo) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '让每一次连接，都有迹可循。' })).toBeVisible()

  for (const [label, className, value] of [
    ['石墨信号', 'theme-graphite-signal', 'graphite-signal'],
    ['极昼控制台', 'theme-arctic-daylight', 'arctic-daylight'],
    ['深海终端', 'theme-deep-ocean', 'deep-ocean'],
  ] as const) {
    await page.getByRole('button', { name: /界面主题/ }).click()
    await page.getByRole('menuitemradio', { name: new RegExp(label) }).click()
    await expect(page.locator('.app-shell')).toHaveClass(new RegExp(className))
    await expect.poll(() => mock.settingsWrites.at(-1)?.uiTheme).toBe(value)
    await saveEvidence(page, testInfo, `dashboard-${value}`)
  }

  assertClean(mock)
})

test('Redis 刷新显示工作区 loading，并支持错误后恢复', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, 'Redis 管理')
  await expect(page.getByRole('heading', { name: '把缓存状态，变成可控现场。' })).toBeVisible()
  await expect(page.getByText('ai-watch:settings')).toBeVisible()

  const releasePromise = mock.delayNextRedisRefresh()
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  const release = await releasePromise
  await expect(page.getByRole('status')).toContainText('正在刷新 Redis 数据')
  release()
  await expect(page.getByRole('status')).toBeHidden()
  await expect(page.getByText('Redis 数据已刷新')).toBeVisible()

  mock.failNextRedisRefresh()
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('模拟 Redis 连接中断')
  expect(mock.consoleErrors.some(message => message.includes('503'))).toBe(true)
  mock.consoleErrors.splice(0)
  await page.getByRole('button', { name: '关闭' }).click()
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(page.getByText('Redis 数据已刷新')).toBeVisible()

  assertClean(mock)
})

test('可靠性页面支持 Provider 对比、时间窗切换和移动端布局', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 320, height: 800 })
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, '可靠性')
  await expect(page.getByRole('heading', { name: '比较每条线路的真实稳定性。' })).toBeVisible()
  await expect(page.getByText('Ray 主线路').first()).toBeVisible()
  await expect(page.getByText('Claude 主线路').first()).toBeVisible()
  await expect(page.getByText('93%')).toBeVisible()
  await expect(page.getByText('推荐主线路')).toBeVisible()
  await expect(page.getByText('建议观察')).toBeVisible()
  await expectNoHorizontalOverflow(page)
  await page.getByRole('button', { name: '7 天' }).click()
  await expect.poll(() => mock.reliabilityRanges.at(-1)).toBe('7d')
  await expect(page.getByText(/7 天 · 42 个样本/)).toBeVisible()
  await saveEvidence(page, testInfo, 'reliability-320')
  assertClean(mock)
})

test('极昼主题覆盖供应商示例和 Diagnostic Bus', async ({ page }, testInfo) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await page.getByRole('button', { name: /界面主题/ }).click()
  await page.getByRole('menuitemradio', { name: /极昼控制台/ }).click()
  const examples = page.locator('details.provider-examples')
  await examples.locator('summary').click()
  await expect(page.getByText('Codex Compatible')).toBeVisible()
  await expect(examples).toHaveCSS('background-color', 'rgb(245, 248, 250)')
  await page.getByRole('button', { name: '新建任务' }).click()
  const choices = page.locator('.choice-card')
  await expect(choices).toHaveCount(4)
  await expect(choices.nth(1)).toHaveCSS('background-color', 'rgb(245, 248, 250)')
  await expect(choices.first()).toHaveCSS('color', 'rgb(23, 44, 54)')
  await page.getByRole('button', { name: '关闭新建任务' }).click()
  await openView(page, '系统诊断')
  const bus = page.locator('.diagnostic-bus')
  await expect(bus).toBeVisible()
  await expect(bus).not.toHaveCSS('background-color', 'rgb(10, 26, 39)')
  await saveEvidence(page, testInfo, 'diagnostics-arctic')
  assertClean(mock)
})

test('计划未知状态不会导致页面黑屏', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, '计划任务')
  await expect(page.getByRole('heading', { name: '计划任务' })).toBeVisible()
  await expect(page.getByText('未知状态 · future_status')).toBeVisible()
  await page.getByRole('button', { name: '查看运行日志：未知状态计划' }).click()
  await expect(page.getByRole('dialog', { name: /未知状态计划/ })).toBeVisible()
  await page.getByText('查看供应商返回与脱敏详情').click()
  await expect(page.getByText('req-schedule-1').first()).toBeVisible()
  await expect(page.getByText('READY')).toBeVisible()
  await page.getByRole('button', { name: '关闭计划运行日志' }).last().click()
  await expect(page.locator('.app-shell')).toBeVisible()
  assertClean(mock)
})

test('测活终端显示脱敏命令、实时输出和返回摘要', async ({ page }, testInfo) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await page.getByRole('button', { name: /Codex · 测活/ }).click()
  await expect(page.locator('.terminal-output')).toContainText('[PROMPT REDACTED]')
  await expect(page.locator('.terminal-output')).toContainText('READY')
  await expect(page.locator('.terminal-output')).toContainText('供应商返回：READY')
  await saveEvidence(page, testInfo, 'terminal-themed')
  await expect.poll(() => page.getByRole('button', { name: '关闭测活终端' }).evaluate(element => getComputedStyle(element).color)).not.toBe('rgb(100, 123, 142)')
  await page.getByRole('button', { name: '关闭终端并返回任务列表' }).click()
  await expect(page.getByRole('dialog', { name: '测活终端输出' })).toBeHidden()
  await page.getByRole('button', { name: /Codex · 测活/ }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '测活终端输出' })).toBeHidden()
  assertClean(mock)
})

test('极昼主题覆盖钉钉配置、计划操作栏和页面底部', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await page.getByRole('button', { name: /界面主题/ }).click()
  await page.getByRole('menuitemradio', { name: /极昼控制台/ }).click()
  await openView(page, '设置与通知')
  await expect(page.locator('.dingtalk-secure-config')).toHaveCSS('background-color', 'rgb(255, 255, 255)')
  await expect(page.getByText('Redis 优先，环境变量回退')).toBeVisible()
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.body).backgroundColor)).toBe('rgb(237, 244, 247)')
  await expect.poll(() => page.evaluate(() => [getComputedStyle(document.documentElement).backgroundColor, getComputedStyle(document.body).backgroundColor, getComputedStyle(document.getElementById('root')!).backgroundColor])).toEqual(['rgb(237, 244, 247)', 'rgb(237, 244, 247)', 'rgb(237, 244, 247)'])
  await openView(page, '计划任务')
  await page.locator('.schedule-check input[type="checkbox"]').nth(1).check({ force: true })
  await expect(page.locator('.bulk-bar')).toHaveCSS('color', 'rgb(23, 44, 54)')
  await expect(page.locator('.bulk-bar')).not.toHaveCSS('background-color', 'rgba(7, 23, 34, 0.96)')
  assertClean(mock)
})

test('可靠性告警设置可以热更新保存', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, '设置与通知')
  await expect(page.getByText('Provider 可靠性告警')).toBeVisible()
  await page.getByRole('checkbox', { name: /启用可靠性告警/ }).check()
  await page.getByLabel('成功率下限').fill('85')
  await page.getByRole('button', { name: '保存设置' }).click()
  await expect.poll(() => mock.settingsWrites.at(-1)?.reliabilityAlertEnabled).toBe(true)
  expect(mock.settingsWrites.at(-1)?.reliabilityAlertSuccessRate).toBe(85)
  assertClean(mock)
})

test('侧边栏 path 支持直接访问和浏览器前进后退', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, '供应商配置')
  await expect(page).toHaveURL(/\/providers$/)
  await openView(page, '可靠性')
  await expect(page).toHaveURL(/\/reliability$/)
  await page.goBack()
  await expect(page).toHaveURL(/\/providers$/)
  await expect(page.getByRole('heading', { name: '供应商配置' })).toBeVisible()
  await page.goForward()
  await expect(page).toHaveURL(/\/reliability$/)
  await expect(page.getByRole('heading', { name: '比较每条线路的真实稳定性。' })).toBeVisible()
  await page.goto('/schedules')
  await expect(page.getByRole('heading', { name: '计划任务' })).toBeVisible()
  await expect(page).toHaveTitle('AI Watch · 计划任务')
  await page.goto('/unknown-page')
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('heading', { name: '让每一次连接，都有迹可循。' })).toBeVisible()
  assertClean(mock)
})

for (const width of viewportWidths) {
  test(`供应商页面在 ${width}px 下无溢出且关键操作可用`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: width <= 375 ? 760 : 900 })
    const mock = await installApiMock(page)
    await page.goto('/')
    await openView(page, '供应商配置')
    await expect(page.getByRole('heading', { name: '供应商配置' })).toBeVisible()
    await expectNoHorizontalOverflow(page)

    const edit = page.getByRole('button', { name: '编辑 Ray 主线路' })
    await expect(edit).toBeVisible()
    await edit.click()
    const dialog = page.getByRole('dialog', { name: '编辑 Ray 主线路' })
    await expect(dialog).toBeVisible()
    await expect(dialog).toHaveCSS('transform', 'none')
    const dialogBounds = await dialog.boundingBox()
    expect(dialogBounds?.x ?? -1).toBeGreaterThanOrEqual(0)
    expect((dialogBounds?.x ?? 0) + (dialogBounds?.width ?? 0)).toBeLessThanOrEqual(width + 1)
    await page.getByRole('button', { name: '关闭', exact: true }).click()
    await saveEvidence(page, testInfo, `providers-${width}`)

    assertClean(mock)
  })
}
