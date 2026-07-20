import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installApiMock, type ApiMock } from './api-mock'

const viewportWidths = [320, 375, 768, 1024, 1440]

async function openView(page: Page, label: string) {
  const centers: Record<string, string> = {
    '供应商配置': 'Provider', '测试场景': '验证中心', '对比历史': '验证中心',
    '计划任务': '自动化', '故障切换': '自动化', '维护窗口': '自动化',
    '可靠性': '稳定性', 'SLO 预算': '稳定性', '事故中心': '稳定性',
    '通知路由': '设置', '系统诊断': '设置', '设置与通知': '设置',
  }
  const center = centers[label] ?? label
  if ((await page.viewportSize())!.width <= 760) {
    await page.getByRole('button', { name: '打开菜单' }).click()
    await expect(page.locator('.sidebar')).toHaveClass(/mobile-open/)
  }
  await page.getByRole('button', { name: center, exact: true }).click()
  const tabLabels: Record<string, string> = { '对比历史': '对比历史', '故障切换': '故障切换', '维护窗口': '维护窗口', 'SLO 预算': 'SLO 与错误预算', '事故中心': '事故与复盘', '通知路由': '通知路由', '系统诊断': '系统诊断' }
  if (tabLabels[label]) await page.getByRole('button', { name: tabLabels[label], exact: true }).click()
}

async function expectNoHorizontalOverflow(page: Page) {
  const sizes = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }))
  expect(sizes.content, `页面宽度 ${sizes.content}px 超出视口 ${sizes.viewport}px`).toBeLessThanOrEqual(sizes.viewport + 1)
}

async function saveEvidence(page: Page, testInfo: TestInfo, name: string) {
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`), animations: 'disabled' })
}

async function confirmDialog(page: Page, label: string) {
  const dialog = page.getByRole('alertdialog')
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: label, exact: true }).click()
  await expect(dialog).toBeHidden()
}

async function expectLightSurface(page: Page, selector: string) {
  const surface = page.locator(selector).first()
  await expect(surface).toBeVisible()
  const colors = await surface.evaluate(element => {
    const style = getComputedStyle(element)
    return { background: style.backgroundColor, color: style.color }
  })
  const channels = (value: string) => (value.match(/[\d.]+/g) || []).slice(0, 3).map(Number)
  const background = channels(colors.background)
  const foreground = channels(colors.color)
  expect(Math.min(...background), `${selector} 背景仍偏暗：${colors.background}`).toBeGreaterThan(210)
  expect(Math.max(...foreground), `${selector} 文字仍偏亮：${colors.color}`).toBeLessThan(150)
}

function assertClean(mock: ApiMock) {
  expect(mock.unmatched).toEqual([])
  expect(mock.consoleErrors).toEqual([])
}

test('键盘可以跳过侧栏且路由切换后焦点进入主内容', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await page.keyboard.press('Tab')
  const skip = page.getByRole('link', { name: '跳到主要内容' })
  await expect(skip).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('main')).toBeFocused()
  await openView(page, 'Provider')
  await expect(page).toHaveURL(/\/providers$/)
  await expect(page.getByRole('main')).toBeFocused()
  assertClean(mock)
})

test('移动端核心处置与筛选按钮保持 44px 触控目标', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 760 })
  const mock = await installApiMock(page)
  for (const [path, heading, selector] of [
    ['/reliability', '比较每条线路的真实稳定性。', '.reliability-remediation-actions button'],
    ['/maintenance', '维护期间继续记录，但暂时保持安静。', '.maintenance-card footer button'],
    ['/slos', '看清可靠性还能消耗多久。', '.slo-card footer button'],
    ['/comparisons', '每一次线路对比，都能重新打开。', '.comparison-filters button'],
  ] as const) {
    await page.goto(path)
    await expect(page.getByRole('heading', { name: heading })).toBeVisible()
    await expect(page.locator(selector).first()).toHaveCSS('min-height', '44px')
    await expectNoHorizontalOverflow(page)
  }
  assertClean(mock)
})

test('移动端各领域可见主操作保持 44px 触控目标', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 760 })
  const mock = await installApiMock(page)
  for (const path of ['/', '/providers', '/events', '/schedules', '/scenarios', '/failover', '/reliability', '/maintenance', '/slos', '/incidents', '/comparisons', '/settings/notifications', '/settings/diagnostics']) {
    await page.goto(path)
    await expect(page.getByRole('main')).toBeVisible()
    const undersized = await page.locator('main button, main a[href], main summary, main .select-trigger').evaluateAll(elements => elements.flatMap(element => {
      const node = element as HTMLElement
      const style = getComputedStyle(node)
      const rect = node.getBoundingClientRect()
      if (style.display === 'none' || style.visibility === 'hidden' || rect.width === 0 || rect.height === 0 || node.closest('[aria-hidden="true"]')) return []
      if (node.matches('[class*="scrim"], .sr-only, .select-native')) return []
      return rect.width < 43.5 || rect.height < 43.5 ? [`${node.tagName.toLowerCase()}.${node.className || '(no-class)'} ${Math.round(rect.width)}×${Math.round(rect.height)} “${node.innerText.trim().slice(0, 24)}”`] : []
    }))
    expect(undersized, `${path} 存在过小主操作`).toEqual([])
    await expectNoHorizontalOverflow(page)
  }
  assertClean(mock)
})

test('各领域正文与辅助说明遵守 12px 和 11px 字号下限', async ({ page }) => {
  const mock = await installApiMock(page)
  for (const path of ['/', '/providers', '/events', '/schedules', '/scenarios', '/failover', '/reliability', '/maintenance', '/slos', '/incidents', '/comparisons', '/settings/notifications', '/settings/diagnostics']) {
    await page.goto(path)
    await expect(page.getByRole('main')).toBeVisible()
    const undersized = await page.locator('main p, main small, main dt').evaluateAll(elements => elements.flatMap(element => {
      const node = element as HTMLElement
      const rect = node.getBoundingClientRect()
      const style = getComputedStyle(node)
      if (style.display === 'none' || style.visibility === 'hidden' || rect.width === 0 || rect.height === 0 || node.closest('[aria-hidden="true"]')) return []
      const floor = node.tagName.toLowerCase() === 'p' ? 12 : 11
      const size = Number.parseFloat(style.fontSize)
      return size + .01 < floor ? [`${node.tagName.toLowerCase()}.${node.className || '(no-class)'} ${size}px “${node.innerText.trim().slice(0, 32)}”`] : []
    }))
    expect(undersized, `${path} 存在低于字号下限的正文或辅助说明`).toEqual([])
  }
  assertClean(mock)
})

test('计划任务与对比历史大列表按 50 条分页', async ({ page }) => {
  const mock = await installApiMock(page)
  mock.seedSchedules(120)
  mock.seedComparisons(120)

  await page.goto('/schedules')
  await expect(page.locator('.schedule-row')).toHaveCount(50)
  const schedulePagination = page.getByRole('navigation', { name: '计划任务分页' })
  await expect(schedulePagination).toContainText('第 1 / 3 页 · 显示 1–50，共 120 条')
  await schedulePagination.getByRole('button', { name: '下一页' }).click()
  await expect(page.locator('.schedule-list').getByText('分页计划 051', { exact: true })).toBeVisible()
  await expect(page.locator('.schedule-list').getByText('分页计划 001', { exact: true })).toHaveCount(0)

  await page.goto('/comparisons')
  await expect(page.locator('.comparison-history-list > button')).toHaveCount(50)
  const comparisonPagination = page.getByRole('navigation', { name: '对比历史分页' })
  await expect(comparisonPagination).toContainText('第 1 / 3 页 · 显示 1–50，共 120 条')
  await comparisonPagination.getByRole('button', { name: '下一页' }).click()
  await expect(page.locator('.comparison-history-list').getByText('分页对比 051', { exact: true })).toBeVisible()
  await expect(page.locator('.comparison-history-list').getByText('分页对比 001', { exact: true })).toHaveCount(0)
  assertClean(mock)
})

test('各领域主流程在多档宽度下无水平溢出', async ({ page }) => {
  test.setTimeout(120_000)
  const mock = await installApiMock(page)
  const paths = ['/', '/providers', '/events', '/schedules', '/scenarios', '/failover', '/reliability', '/maintenance', '/slos', '/incidents', '/comparisons', '/settings/notifications', '/settings/diagnostics']
  for (const width of [320, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    for (const path of paths) {
      await page.goto(path)
      await expect(page.getByRole('main')).toBeVisible()
      await expectNoHorizontalOverflow(page)
    }
  }
  assertClean(mock)
})

test('三套主题的文字、状态、焦点与表单边界满足对比度基线', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/schedules')

  for (const label of ['深海终端', '石墨信号', '极昼控制台']) {
    await page.getByRole('button', { name: /界面主题/ }).click()
    await page.getByRole('menuitemradio', { name: new RegExp(label) }).click()
    const ratios = await page.locator('.app-shell').evaluate(shell => {
      type Color = [number, number, number, number]
      const parse = (value: string): Color => {
        const source = value.trim()
        if (source.startsWith('#')) {
          const hex = source.slice(1)
          const full = hex.length === 3 ? hex.split('').map(char => char + char).join('') : hex
          return [Number.parseInt(full.slice(0, 2), 16), Number.parseInt(full.slice(2, 4), 16), Number.parseInt(full.slice(4, 6), 16), 1]
        }
        const values = source.match(/[\d.]+/g)?.map(Number) || [0, 0, 0, 0]
        return [values[0], values[1], values[2], values[3] ?? 1]
      }
      const over = (foreground: Color, background: Color): Color => {
        const alpha = foreground[3] + background[3] * (1 - foreground[3])
        if (!alpha) return [0, 0, 0, 0]
        return [0, 1, 2].map(index => (foreground[index] * foreground[3] + background[index] * background[3] * (1 - foreground[3])) / alpha).concat(alpha) as Color
      }
      const luminance = (color: Color) => {
        const channels = color.slice(0, 3).map(value => value / 255).map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4)
        return .2126 * channels[0] + .7152 * channels[1] + .0722 * channels[2]
      }
      const contrast = (foreground: Color, background: Color) => {
        const [high, low] = [luminance(foreground), luminance(background)].sort((a, b) => b - a)
        return (high + .05) / (low + .05)
      }
      const style = getComputedStyle(shell)
      const token = (name: string) => parse(style.getPropertyValue(name))
      const surface1 = token('--surface-1')
      const surface2 = token('--surface-2')
      const control = token('--control-bg')
      const statusRatio = (name: string) => contrast(token(name), over(token(`${name}-soft`), surface1))
      return {
        primary: contrast(token('--text-primary'), surface1),
        secondary: contrast(token('--text-secondary'), surface2),
        muted: contrast(token('--text-muted'), surface2),
        info: statusRatio('--status-info'),
        success: statusRatio('--status-success'),
        warning: statusRatio('--status-warning'),
        danger: statusRatio('--status-danger'),
        focus: contrast(token('--status-info'), surface1),
        controlBoundary: contrast(token('--control-border'), control),
      }
    })
    for (const [name, ratio] of Object.entries(ratios)) {
      const minimum = name === 'controlBoundary' ? 3 : 4.5
      expect(ratio, `${label} 的 ${name} 对比度不足`).toBeGreaterThanOrEqual(minimum)
    }

    const refresh = page.locator('.schedule-refresh')
    await expect(refresh).toBeEnabled()
    await page.locator('.schedule-filter-group button').last().focus()
    await page.keyboard.press('Tab')
    await expect(refresh).toBeFocused()
    await expect(refresh).toHaveCSS('outline-style', 'solid')

    const disabled = page.locator('.schedule-terminal-button:disabled').first()
    await expect(disabled).toBeVisible()
    expect(Number(await disabled.evaluate(element => getComputedStyle(element).opacity))).toBeLessThanOrEqual(.65)
    await expect(disabled).toHaveCSS('cursor', 'not-allowed')

    await page.getByRole('button', { name: '新建计划' }).click()
    const input = page.getByRole('dialog').getByLabel('计划名称')
    const actualBoundary = await input.evaluate(element => {
      const parse = (value: string) => (value.match(/[\d.]+/g) || []).slice(0, 3).map(Number)
      const luminance = (color: number[]) => {
        const channels = color.map(value => value / 255).map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4)
        return .2126 * channels[0] + .7152 * channels[1] + .0722 * channels[2]
      }
      const style = getComputedStyle(element)
      const values = [luminance(parse(style.borderTopColor)), luminance(parse(style.backgroundColor))].sort((a, b) => b - a)
      return (values[0] + .05) / (values[1] + .05)
    })
    expect(actualBoundary, `${label} 的真实输入框边界对比度不足`).toBeGreaterThanOrEqual(3)
    await page.getByRole('dialog').getByRole('button', { name: '取消' }).click()
  }
  assertClean(mock)
})

test('reduced-motion 会关闭关键弹层和状态动效', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const mock = await installApiMock(page)
  await page.goto('/schedules')
  await page.getByRole('button', { name: '新建计划' }).click()
  const trigger = page.getByRole('dialog').locator('.select-trigger').first()
  await trigger.click()
  const popover = page.locator('.select-popover')
  await expect(popover).toBeVisible()
  await expect(popover).toHaveCSS('animation-name', 'none')
  expect(await trigger.evaluate(element => Number.parseFloat(getComputedStyle(element).transitionDuration) || 0)).toBeLessThanOrEqual(.01)
  await page.keyboard.press('Escape')
  await page.getByRole('dialog').getByRole('button', { name: '取消' }).click()
  assertClean(mock)
})

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

test('极昼主题覆盖七个领域中心的主要卡片表面', async ({ page }, testInfo) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await page.getByRole('button', { name: /界面主题/ }).click()
  await page.getByRole('menuitemradio', { name: /极昼控制台/ }).click()

  for (const [view, selector] of [
    ['总览', '.metric-card'],
    ['Provider', '.provider-vault-summary'],
    ['验证中心', '.scenario-card'],
    ['自动化', '.schedule-summary'],
    ['稳定性', '.reliability-advice-card'],
    ['事件记录', '.event-summary'],
    ['设置', '.settings-panel'],
  ] as const) {
    await openView(page, view)
    await expectLightSurface(page, selector)
  }
  for (const [view, selector] of [
    ['对比历史', '.comparison-history-list'],
    ['故障切换', '.failover-card'],
    ['维护窗口', '.maintenance-card'],
    ['SLO 预算', '.slo-card'],
    ['事故中心', '.incident-list'],
    ['通知路由', '.notification-routing-summary'],
    ['系统诊断', '.diagnostics-panel'],
  ] as const) {
    await openView(page, view)
    await expectLightSurface(page, selector)
  }
  await saveEvidence(page, testInfo, 'settings-arctic-surface-audit')
  assertClean(mock)
})

test('首页行动中心聚合事故、异常线路和计划并支持路由处置', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await expect(page.getByText('需要处理', { exact: true })).toBeVisible()
  await expect(page.getByText('Claude 主备组 请求连续失败')).toBeVisible()
  await expect(page.getByText('成功率 88%，低于 90%')).toBeVisible()
  await expect(page.getByText('未知状态计划')).toBeVisible()
  await page.getByRole('button', { name: '查看事故' }).click()
  await expect(page).toHaveURL(/\/incidents$/)
  await page.goBack()
  await expect(page.getByText('需要处理', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '打开计划' }).first().click()
  await expect(page).toHaveURL(/\/schedules$/)
  assertClean(mock)
})

test('首页行动中心局部数据失败不会让首页黑屏', async ({ page }) => {
  const mock = await installApiMock(page)
  mock.failNextActionCenter()
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '让每一次连接，都有迹可循。' })).toBeVisible()
  await expect(page.getByText('事故数据暂不可用，其他结果仍可操作。')).toBeVisible()
  await expect(page.getByText('成功率 88%，低于 90%')).toBeVisible()
  expect(mock.consoleErrors.some(message => message.includes('503'))).toBe(true)
  mock.consoleErrors.splice(0)
  assertClean(mock)
})

test('首页运行中的任务会在可见状态下近实时刷新', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await expect(page.locator('.system-pulse').getByText('0', { exact: true })).toBeVisible()
  await expect(page.locator('.job-list .job-row')).toHaveCount(0)

  mock.setJobStatus('running')
  await expect(page.locator('.system-pulse').getByText('1', { exact: true })).toBeVisible({ timeout: 5000 })
  await expect(page.locator('.job-list .job-row')).toHaveCount(1)
  await expect(page.locator('.action-item.kind-job')).toBeVisible()

  mock.setJobStatus('success')
  await expect(page.locator('.system-pulse').getByText('0', { exact: true })).toBeVisible({ timeout: 5000 })
  await expect(page.locator('.job-list .job-row')).toHaveCount(0)
  await expect(page.locator('.action-item.kind-job')).toHaveCount(0)
  assertClean(mock)
})

test('首页行动中心停止任务后同步刷新全部运行任务指标', async ({ page }) => {
  const mock = await installApiMock(page)
  mock.setJobStatus('running')
  await page.goto('/')
  const jobAction = page.locator('.action-item.kind-job')
  await expect(jobAction).toBeVisible()
  await jobAction.getByRole('button', { name: '停止' }).click()
  await expect.poll(() => mock.stopJobCalls).toEqual(['job-1'])
  await expect(page.locator('.system-pulse').getByText('0', { exact: true })).toBeVisible()
  await expect(page.locator('.job-list .job-row')).toHaveCount(0)
  await expect(jobAction).toHaveCount(0)
  assertClean(mock)
})

test('测试场景页面展示内置场景并支持多线路对比', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/scenarios')
  await expect(page.getByRole('heading', { name: '用同一把尺子，比较每条线路。' })).toBeVisible()
  await expect(page.getByText('基础可用性')).toBeVisible()
  await expect(page.getByText('JSON 格式遵循')).toBeVisible()
  await page.getByRole('button', { name: '多线路对比' }).first().click()
  await expect(page.getByRole('dialog', { name: '基础可用性' })).toBeVisible()
  await page.getByRole('button', { name: 'Claude', exact: true }).click()
  await expect(page.getByText('当前 Claude 配置')).toBeVisible()
  await expect(page.getByText('Claude 主线路')).toBeVisible()
  await page.getByRole('button', { name: '运行所选线路' }).click()
  await expect(page.getByText('3 / 3 通过')).toBeVisible()
  await expect(page.getByText('0.7s')).toBeVisible()
  await expect(page.getByRole('button', { name: '请求详情' }).first()).toBeVisible()
  await page.getByRole('button', { name: '请求详情' }).first().click()
  await expect(page).toHaveURL(/\/requests\/req-comparison-/)
  await page.goBack()
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('scenario_comparison')

  assertClean(mock)
})

test('对比历史支持深链接、请求详情和按原集合重跑', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/comparisons')
  await expect(page.getByRole('heading', { name: '每一次线路对比，都能重新打开。' })).toBeVisible()
  await expect(page.getByText('基础可用性').first()).toBeVisible()
  await expect(page.getByText('Claude 主线路')).toBeVisible()
  await page.locator('.comparison-history-list>button').first().click()
  await expect(page).toHaveURL(/\/comparisons\/comparison-1$/)
  await expect(page.getByRole('button', { name: '请求详情' }).first()).toBeVisible()
  await page.getByRole('button', { name: '按原集合重跑' }).click()
  await confirmDialog(page, '开始重跑')
  await expect(page).toHaveURL(/\/validation\/comparisons\/comparison-2$/)
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('scenario_comparison_rerun')
  await page.goBack()
  await expect(page).toHaveURL(/\/validation\/comparisons\/comparison-1$/)
  await page.getByRole('button', { name: '请求详情' }).first().click()
  await expect(page).toHaveURL(/\/requests\/req-comparison-1$/)
  assertClean(mock)
})

test('自动故障切换组展示活跃线路、安全边界和维护窗口', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/failover')
  const card = page.locator('.failover-card').filter({ hasText: 'Claude 主备组' })
  await expect(page.getByRole('heading', { name: '先验证备用线路，再做切换决定。' })).toBeVisible()
  await expect(page.getByText('Claude 主备组')).toBeVisible()
  await expect(card.getByText('Claude 主线路')).toBeVisible()
  await expect(card.getByText('Claude 备用线路').first()).toBeVisible()
  await expect(card.getByText('自动切换组内计划')).toBeVisible()
  await expect(card.getByText('当前活跃').locator('..').getByText('Claude 备用线路')).toBeVisible()
  await expect(card.getByText('主线恢复探测').locator('..')).toContainText('成功')
  await card.getByRole('button', { name: '验证备用线路' }).click()
  await expect(page.getByText('备用线路验证已启动；通过后将切换绑定此组的计划')).toBeVisible()
  await expect(page.locator('.failover-state.validating')).toHaveText('验证中')
  await expect(page.getByText('AI Watch 不会修改 Codex、Claude、CC Switch 或宿主机配置')).toBeVisible()
  await card.getByRole('button', { name: '编辑' }).click()
  const failoverDialog = page.getByRole('dialog', { name: 'Claude 主备组' })
  await expect(failoverDialog.locator('label').filter({ hasText: '切换模式' }).locator('select')).toHaveValue('automatic')
  await expect(page.getByText('只切换绑定此组的 AI Watch 计划')).toBeVisible()
  await expect(failoverDialog.locator('label').filter({ hasText: '主线路恢复探测间隔' }).locator('input')).toHaveValue('300')
  await expect(failoverDialog.getByText('维护窗口截止（可选）')).toHaveCount(0)
  assertClean(mock)
})

test('计划任务可以显式绑定自动故障切换组', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/schedules')
  await page.getByRole('button', { name: '新建计划' }).click()
  const dialog = page.getByRole('dialog', { name: '新建计划任务' })
  await dialog.locator('label').filter({ hasText: '客户端' }).locator('select').selectOption('claude')
  await dialog.locator('label').filter({ hasText: 'Provider Group（可选）' }).locator('select').selectOption('claude-main')
  const activeProvider = dialog.locator('label').filter({ hasText: '当前活跃 Provider' }).locator('select')
  await expect(activeProvider).toBeDisabled()
  await expect(activeProvider).toHaveValue('cc-switch:claude-backup')
  await expect(page.getByText('计划每次运行时读取组内当前活跃线路；自动模式只会切换绑定此组的计划。')).toBeVisible()
  assertClean(mock)
})

test('建议模式可以人工采用已验证建议并显示影响范围', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/failover')
  const card = page.locator('.failover-card').filter({ hasText: 'Claude 建议切换组' })
  await expect(card.getByText('建议切换', { exact: true })).toBeVisible()
  await expect(card.getByText('validation-request')).toBeVisible()
  await card.getByRole('button', { name: '采用建议' }).click()
  const dialog = page.getByRole('alertdialog', { name: '采用“Claude 建议切换组”的切换建议？' })
  await expect(dialog).toContainText('Claude 备用线路')
  await expect(dialog).toContainText('1 条计划')
  await expect(dialog).toContainText('不会修改 Codex、Claude 或 CC Switch')
  await dialog.getByRole('button', { name: '确认采用建议' }).click()
  await expect(page.getByText('已切换 1 条绑定计划')).toBeVisible()
  await expect(card.locator('.failover-state.applied')).toHaveText('已采用')
  await expect(card.getByText('当前活跃').locator('..')).toContainText('Claude 备用线路')
  expect(mock.providerGroupActions).toEqual(['apply_advice'])
  assertClean(mock)
})

test('事故中心聚合故障并支持确认、备注和关联请求跳转', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/incidents')
  await expect(page.getByRole('heading', { name: '把重复故障，收敛成一条清晰时间线。' })).toBeVisible()
  await expect(page.getByText('Claude 主备组 请求连续失败')).toBeVisible()
  await expect(page.getByText('3 次失败')).toBeVisible()
  await page.getByText('Claude 主备组 请求连续失败').click()
  await expect(page.getByLabel('Claude 主备组 请求连续失败 事故详情')).toBeVisible()
  await page.getByRole('button', { name: '确认事故' }).click()
  await expect(page.locator('.incident-detail .incident-status')).toHaveText('已确认')
  await page.getByPlaceholder('记录判断、临时措施或后续行动…').fill('已联系备用供应商并持续观察。')
  await page.getByRole('button', { name: '保存备注' }).click()
  await expect(page.getByPlaceholder('记录判断、临时措施或后续行动…')).toHaveValue('已联系备用供应商并持续观察。')
  await page.getByRole('button', { name: '请求 req-schedule-1' }).click()
  await expect(page).toHaveURL(/\/requests\/req-schedule-1$/)
  assertClean(mock)
})

test('事故复盘支持快照、人工结论、完成和 Markdown', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/incidents')
  await page.getByRole('button', { name: /界面主题/ }).click()
  await page.getByRole('menuitemradio', { name: /极昼控制台/ }).click()
  await page.getByText('Claude 主备组 请求连续失败').click()
  await page.getByRole('button', { name: '事故复盘' }).click()
  const dialog = page.getByRole('dialog', { name: 'Claude 主备组 请求连续失败' })
  await expect(dialog.getByText('事实证据已封存')).toBeVisible()
  await expect(dialog.getByText('供应商请求超时')).toBeVisible()
  await dialog.getByLabel('根因').fill('上游网关超时')
  await dialog.getByLabel('处置总结').fill('切换备用线路')
  await dialog.getByLabel('复盘负责人').fill('ops')
  await dialog.getByRole('button', { name: '添加' }).click()
  await dialog.getByLabel('行动 1', { exact: true }).fill('增加网关超时告警')
  await dialog.getByLabel('行动 1 负责人').fill('ops')
  await dialog.getByRole('button', { name: '保存草稿' }).click()
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('postmortem:save')
  await dialog.getByRole('button', { name: '标记完成' }).click()
  await expect(dialog.getByText('已完成', { exact: true })).toBeVisible()
  await expect(dialog.getByLabel('根因')).toBeDisabled()
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('postmortem:complete')
  await dialog.getByRole('button', { name: '复制 Markdown' }).click()
  await expect(dialog.getByRole('status')).toContainText('Markdown 已复制')
  await expect(dialog).not.toHaveCSS('background-color', 'rgb(7, 23, 34)')
  await page.setViewportSize({ width: 375, height: 760 })
  await expectNoHorizontalOverflow(page)
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
  await expect(page.getByText('建议暂停')).toBeVisible()
  await expect(page.getByRole('button', { name: '导出报告' })).toHaveCSS('white-space', 'nowrap')
  await expect(page.getByText(/24 小时内共 42 次请求/)).toBeVisible()
  const trendData = page.getByText('查看 24 个时间桶的数据表')
  await trendData.focus()
  await page.keyboard.press('Enter')
  const trendTable = page.getByRole('table', { name: '24 小时可靠性趋势明细' })
  await expect(trendTable).toBeVisible()
  await expect(trendTable.getByRole('columnheader')).toHaveCount(7)
  await expect(trendTable.getByRole('row')).toHaveCount(25)
  await expect(page.getByRole('link', { name: '最近请求' })).toHaveAttribute('href', '/requests/req-schedule-1')
  await page.getByLabel('报告格式').selectOption('json')
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出报告' }).click()
  await download
  expect(mock.reliabilityExports.at(-1)).toBe('24h:json')
  await expectNoHorizontalOverflow(page)
  await page.getByRole('button', { name: '7 天' }).click()
  await expect.poll(() => mock.reliabilityRanges.at(-1)).toBe('7d')
  await expect(page.getByText(/7 天 · 42 个样本/)).toBeVisible()
  await saveEvidence(page, testInfo, 'reliability-320')
  assertClean(mock)
})

test('可靠性建议支持复测、备用验证、相关计划和确认暂停', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/reliability')
  const claudeCard = page.locator('.reliability-advice-card').filter({ hasText: 'Claude 主线路' })
  await expect(claudeCard.getByRole('button', { name: '立即复测' })).toBeVisible()
  await expect(claudeCard.getByRole('button', { name: '测试备用' })).toBeVisible()
  await expect(claudeCard.getByRole('button', { name: '相关计划' })).toBeVisible()
  await expect(claudeCard.getByRole('button', { name: '暂停 1 个计划' })).toBeVisible()
  await claudeCard.getByRole('button', { name: '立即复测' }).click()
  await expect(page.getByRole('status')).toContainText('Claude 主线路 复测已启动')
  await expect.poll(() => mock.reliabilityActions.at(-1)).toBe('cc-switch:claude-main:retest')
  await claudeCard.getByRole('button', { name: '测试备用' }).click()
  await expect(page.getByRole('status')).toContainText('备用线路验证已启动')
  await expect.poll(() => mock.reliabilityActions.at(-1)).toBe('cc-switch:claude-main:validate_backup')
  await claudeCard.getByRole('button', { name: '暂停 1 个计划' }).click()
  await confirmDialog(page, '暂停 1 个计划')
  await expect(page.getByRole('status')).toContainText('已暂停 1 个相关计划')
  await expect.poll(() => mock.reliabilityActions.at(-1)).toBe('cc-switch:claude-main:pause_schedules')
  await page.getByRole('button', { name: '相关计划' }).click()
  await expect(page).toHaveURL(/\/schedules$/)
  assertClean(mock)
})

test('极昼主题覆盖任务选择和 Diagnostic Bus', async ({ page }, testInfo) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await page.getByRole('button', { name: /界面主题/ }).click()
  await page.getByRole('menuitemradio', { name: /极昼控制台/ }).click()
  await page.getByRole('button', { name: '新建任务' }).click()
  const choices = page.locator('.choice-card')
  await expect(choices).toHaveCount(4)
  await expect(choices.nth(1)).toHaveCSS('background-color', 'rgb(245, 248, 250)')
  await expect(choices.first()).toHaveCSS('color', 'rgb(23, 44, 54)')
  await page.getByRole('button', { name: '继续' }).click()
  await page.getByRole('button', { name: '继续' }).click()
  const sourceCard = page.locator('.provider-card').first()
  await expect(sourceCard).toBeVisible()
  await expect(sourceCard).toHaveCSS('color', 'rgb(23, 44, 54)')
  await expect.poll(() => sourceCard.evaluate(element => getComputedStyle(element).backgroundImage)).toContain('rgb(245, 248, 250)')
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
  await page.getByRole('button', { name: '停止运行：运行中计划' }).click()
  await expect(page.getByText('已停止并暂停计划规则：运行中计划')).toBeVisible()
  await page.getByRole('button', { name: '查看运行日志：未知状态计划' }).click()
  await expect(page.getByRole('dialog', { name: /未知状态计划/ })).toBeVisible()
  await page.getByText('查看供应商返回与脱敏详情').click()
  await expect(page.getByText('req-schedule-1').first()).toBeVisible()
  await expect(page.getByText('READY')).toBeVisible()
  await expect(page.getByRole('button', { name: '打开完整请求详情' })).toBeVisible()
  await page.getByRole('button', { name: '关闭计划运行日志' }).last().click()
  await expect(page.locator('.app-shell')).toBeVisible()
  assertClean(mock)
})

test('计划任务终端支持最近任务回放、无任务禁用和读取失败恢复', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/schedules')

  const replayButton = page.getByRole('button', { name: '查看实时终端：未知状态计划' })
  await expect(replayButton).toBeEnabled()
  await expect(replayButton).toHaveAttribute('title', '回放最近一轮终端输出')
  await replayButton.click()
  await expect(page.getByRole('dialog', { name: '测活终端输出' })).toBeVisible()
  await expect(page.locator('.terminal-output')).toContainText('[PROMPT REDACTED]')
  await expect(page.locator('.terminal-output')).toContainText('供应商返回：READY')
  await page.getByRole('button', { name: '关闭终端并返回任务列表' }).click()

  const emptyButton = page.getByRole('button', { name: '查看实时终端：Claude 建议组巡检' })
  await expect(emptyButton).toBeDisabled()
  await expect(emptyButton).toHaveAttribute('title', '尚无可回放的终端输出')

  mock.failNextJobRead()
  await replayButton.click()
  await expect(page.getByText('最近任务已不可用，可等待下一次运行')).toBeVisible()
  await expect(page.getByRole('heading', { name: '计划任务' })).toBeVisible()
  await expect(page.getByRole('button', { name: '查看运行日志：未知状态计划' })).toBeEnabled()
  expect(mock.consoleErrors).toContain('Failed to load resource: the server responded with a status of 404 (Not Found)')
  mock.consoleErrors.splice(0)
  assertClean(mock)
})

test('计划任务在终端停止后返回列表无需再次停止', async ({ page }) => {
  const mock = await installApiMock(page)
  mock.setJobStatus('running')
  await page.goto('/schedules')

  const row = page.locator('.schedule-row').filter({ hasText: '运行中计划' })
  await row.getByRole('button', { name: '查看实时终端：运行中计划' }).click()
  const terminal = page.getByRole('dialog', { name: '测活终端输出' })
  await expect(terminal.getByRole('button', { name: '停止任务' })).toBeVisible()
  await terminal.getByRole('button', { name: '停止任务' }).click()
  await expect.poll(() => mock.stopJobCalls).toEqual(['job-1'])
  await expect(terminal.getByText('已停止')).toBeVisible()
  await terminal.getByRole('button', { name: '返回并关闭终端' }).click()

  await expect(row).toHaveAttribute('data-status', 'stopped')
  await expect(row.getByRole('button', { name: '停止运行：运行中计划' })).toHaveCount(0)
  await expect(row.getByRole('button', { name: '暂停：运行中计划' })).toBeVisible()
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
  await expect(page.locator('.dingtalk-config-card')).toHaveCSS('background-color', 'rgb(255, 255, 255)')
  await expect(page.getByText('Redis 优先，环境变量回退')).toBeVisible()
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.body).backgroundColor)).toBe('rgb(237, 244, 247)')
  await expect.poll(() => page.evaluate(() => [getComputedStyle(document.documentElement).backgroundColor, getComputedStyle(document.body).backgroundColor, getComputedStyle(document.getElementById('root')!).backgroundColor])).toEqual(['rgb(237, 244, 247)', 'rgb(237, 244, 247)', 'rgb(237, 244, 247)'])
  await openView(page, '计划任务')
  await page.locator('.schedule-check input[type="checkbox"]').nth(1).check({ force: true })
  await expect(page.locator('.bulk-bar')).toHaveCSS('color', 'rgb(23, 44, 54)')
  await expect(page.locator('.bulk-bar')).not.toHaveCSS('background-color', 'rgba(7, 23, 34, 0.96)')
  assertClean(mock)
})

test('极昼主题下本地供应商测活按钮保持清晰层级和交互反馈', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/providers')
  await page.getByRole('button', { name: /界面主题/ }).click()
  await page.getByRole('menuitemradio', { name: /极昼控制台/ }).click()

  const probe = page.getByRole('button', { name: '测活：Ray 主线路' })
  await expect(probe).toBeVisible()
  await expect(probe).toHaveCSS('color', 'rgb(7, 119, 123)')
  await expect(probe).toHaveCSS('min-height', '44px')
  await expect.poll(() => probe.evaluate(element => getComputedStyle(element).backgroundImage)).toContain('linear-gradient')
  await probe.hover()
  await expect(probe).toHaveCSS('color', 'rgb(255, 255, 255)')
  await expect(probe).toHaveCSS('background-color', 'rgb(7, 119, 123)')
  assertClean(mock)
})

test('可靠性告警设置可以热更新保存', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, '设置与通知')
  await expect(page.getByText('Provider 可靠性告警')).toBeVisible()
  await expect(page.getByText('连续失败间隔是必选门槛')).toBeVisible()
  await expect(page.getByText(/成功率和 P95 不会独立触发/)).toBeVisible()
  await expect(page.getByLabel('其他告警冷却')).toHaveCount(0)
  await page.getByRole('checkbox', { name: /启用可靠性告警/ }).check()
  await page.getByLabel('连续失败告警间隔').fill('4')
  await page.getByLabel('成功率下限').fill('0.01')
  await page.getByRole('button', { name: '保存设置' }).click()
  await expect.poll(() => mock.settingsWrites.at(-1)?.reliabilityAlertEnabled).toBe(true)
  expect(mock.settingsWrites.at(-1)?.reliabilityAlertSuccessRate).toBe(0.01)
  expect(mock.settingsWrites.at(-1)?.reliabilityAlertConsecutiveFailures).toBe(4)
  assertClean(mock)
})

test('代理订阅可以在系统设置中保存测试并清除', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, '设置与通知')

  const panel = page.locator('.proxy-subscription-panel')
  await expect(panel.getByText('代理订阅', { exact: true })).toBeVisible()
  await expect(panel.getByText('未配置', { exact: true })).toBeVisible()
  const input = panel.getByLabel('Mihomo 订阅地址')
  mock.failNextProxyApply()
  await input.fill('https://subscription.example/broken?token=hidden-error-secret')
  await panel.getByRole('button', { name: '保存并应用' }).click()
  await expect(panel.getByText('失败阶段：订阅加载')).toBeVisible()
  await expect(panel.getByText('订阅未返回可用代理节点')).toBeVisible()
  await expect(panel.getByText(/hidden-error-secret/)).toHaveCount(0)
  mock.consoleErrors.length = 0

  await input.fill('https://subscription.example/private?token=page-secret')
  await panel.getByRole('button', { name: '保存并应用' }).click()
  await expect(panel.getByText('订阅已加密保存并应用')).toBeVisible()
  await expect(panel.getByText('已应用', { exact: true })).toBeVisible()
  await expect(panel.getByText('Hong Kong Auto')).toBeVisible()
  await expect(input).toHaveValue('')
  await expect(panel.getByText(/page-secret/)).toHaveCount(0)
  expect(mock.bulkActions).toContain('proxy:save')

  await panel.getByRole('button', { name: '重新测试' }).click()
  await expect(panel.getByText('代理连通测试通过')).toBeVisible()
  expect(mock.bulkActions).toContain('proxy:test')

  await panel.getByRole('button', { name: '清除订阅' }).click()
  await confirmDialog(page, '清除订阅')
  await expect(panel.getByText('订阅已清除，基础代理配置已恢复')).toBeVisible()
  await expect(panel.getByText('未配置', { exact: true })).toBeVisible()
  expect(mock.bulkActions).toContain('proxy:clear')
  assertClean(mock)
})

test('定时可靠性摘要可以配置、保存和预览', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, '设置与通知')
  await expect(page.getByText('定时可靠性摘要')).toBeVisible()
  await page.getByRole('checkbox', { name: /启用每日自动摘要/ }).check()
  await page.getByLabel('发送时间').fill('18:35')
  await page.getByLabel('时区').selectOption('Asia/Tokyo')
  await page.getByLabel('统计范围').selectOption('7d')
  await page.getByRole('button', { name: '保存设置' }).click()
  await expect.poll(() => mock.settingsWrites.at(-1)?.reliabilityDigestEnabled).toBe(true)
  expect(mock.settingsWrites.at(-1)).toMatchObject({ reliabilityDigestHour: 18, reliabilityDigestMinute: 35, reliabilityDigestTimezone: 'Asia/Tokyo', reliabilityDigestRange: '7d' })
  await page.getByRole('button', { name: '立即预览' }).click()
  await expect(page.locator('.reliability-digest-preview')).toContainText('整体成功率：99.5%')
  await expect(page.getByRole('button', { name: '立即发送' })).toBeDisabled()
  assertClean(mock)
})

test('侧边栏 path 支持直接访问和浏览器前进后退', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/')
  await openView(page, '供应商配置')
  await expect(page).toHaveURL(/\/providers$/)
  await openView(page, '可靠性')
  await expect(page).toHaveURL(/\/stability\/reliability$/)
  await page.goBack()
  await expect(page).toHaveURL(/\/providers$/)
  await expect(page.getByRole('heading', { name: '供应商配置' })).toBeVisible()
  await page.goForward()
  await expect(page).toHaveURL(/\/stability\/reliability$/)
  await expect(page.getByRole('heading', { name: '比较每条线路的真实稳定性。' })).toBeVisible()
  await page.goto('/schedules')
  await expect(page.getByRole('heading', { name: '计划任务' })).toBeVisible()
  await expect(page).toHaveTitle('AI Watch · 计划任务')
  await page.goto('/unknown-page')
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('heading', { name: '让每一次连接，都有迹可循。' })).toBeVisible()
  assertClean(mock)
})

test('统一请求详情支持深链接、事件入口和浏览器返回', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/requests/req-schedule-1')
  await expect(page).toHaveURL(/\/requests\/req-schedule-1$/)
  await expect(page.getByRole('heading', { name: 'ray' })).toBeVisible()
  await expect(page.getByText('READY')).toBeVisible()
  await expect(page.getByText('[PROMPT REDACTED]')).toBeVisible()
  await expect(page.getByText('请求成功，无需重试')).toBeVisible()
  await page.getByRole('button', { name: '返回' }).click()
  await expect(page).toHaveURL(/\/events$/)
  await page.getByRole('button', { name: /请求日志/ }).click()
  await page.getByText('req-schedule-1').first().click()
  await page.getByRole('button', { name: '打开完整请求详情' }).click()
  await expect(page).toHaveURL(/\/requests\/req-schedule-1$/)
  await page.goBack()
  await expect(page).toHaveURL(/\/events$/)
  assertClean(mock)
})

test('维护窗口支持开始、延长、提前结束和未来调度', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/maintenance')
  await expect(page).toHaveURL(/\/maintenance$/)
  await expect(page.getByRole('heading', { name: '维护期间继续记录，但暂时保持安静。' })).toBeVisible()

  const card = page.locator('.maintenance-card').filter({ hasText: 'Claude 主备组' })
  await expect(card).toContainText('进行中')
  await card.getByRole('button', { name: '提前结束' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '结束维护' }).click()
  await expect(card).toContainText('未设置')
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('maintenance:end')

  await card.getByRole('button', { name: '30 分钟' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '开启 30 分钟' }).click()
  await expect(card).toContainText('进行中')
  await expect(page.getByRole('status')).toContainText('维护窗口已开始')
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('maintenance:start')
  await card.getByRole('button', { name: '延长 30 分钟' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '延长 30 分钟' }).click()
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('maintenance:extend')
  await card.getByRole('button', { name: '提前结束' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '结束维护' }).click()
  await expect(card).toContainText('未设置')

  await card.getByRole('button', { name: '自定义' }).click()
  const dialog = page.getByRole('dialog', { name: 'Claude 主备组' })
  await dialog.getByLabel('开始时间').fill('2030-07-15T10:00')
  await dialog.getByLabel('结束时间').fill('2030-07-15T11:00')
  await dialog.getByRole('button', { name: '设置窗口' }).click()
  await expect(dialog).toBeHidden()
  await expect(card).toContainText('即将开始')
  await expect(card).toContainText('窗口尚未开始，当前行为不受影响')
  assertClean(mock)
})

test('SLO 错误预算支持配置、暂停、恢复和路由跳转', async ({ page }) => {
  const mock = await installApiMock(page)
  await page.goto('/slos')
  await expect(page).toHaveURL(/\/slos$/)
  await expect(page.getByRole('heading', { name: '看清可靠性还能消耗多久。' })).toBeVisible()
  const card = page.locator('.slo-card').filter({ hasText: 'Claude 主备组' })
  await expect(card).toContainText('已暂停')
  await card.getByRole('button', { name: '设置目标' }).click()
  const dialog = page.getByRole('dialog', { name: 'Claude 主备组' })
  await dialog.getByLabel('目标成功率（%）').fill('99')
  await dialog.getByLabel('滚动窗口').selectOption('24h')
  await dialog.getByLabel('最小有效样本').fill('50')
  await dialog.getByRole('button', { name: '保存目标' }).click()
  await expect(dialog).toBeHidden()
  await expect(card).toContainText('24h 滚动窗口')
  await expect(card).toContainText('错误预算剩余')
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('slo:configure')
  await card.getByRole('button', { name: '暂停' }).click()
  await confirmDialog(page, '暂停计算')
  await expect(card).toContainText('已暂停')
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('slo:pause')
  await card.getByRole('button', { name: '恢复' }).click()
  await expect.poll(() => mock.bulkActions.at(-1)).toBe('slo:resume')
  await page.getByRole('button', { name: /界面主题/ }).click()
  await page.getByRole('menuitemradio', { name: /极昼控制台/ }).click()
  await expect(page.locator('.app-shell')).toHaveClass(/theme-arctic-daylight/)
  await expect(card).not.toHaveCSS('background-color', 'rgb(7, 23, 34)')
  await page.setViewportSize({ width: 375, height: 760 })
  await expectNoHorizontalOverflow(page)
  await card.getByRole('button', { name: '可靠性' }).click()
  await expect(page).toHaveURL(/\/reliability$/)
  assertClean(mock)
})

test('通知路由中心支持加密渠道、分流、测试和删除回退', async ({ page }) => {
  const mock=await installApiMock(page)
  await page.goto('/notification-routing')
  await expect(page).toHaveURL(/\/settings\/notifications$/)
  await expect(page.getByRole('heading',{name:'让每类消息，抵达正确的人。'})).toBeVisible()
  await page.getByRole('button',{name:'新增渠道'}).click()
  const dialog=page.getByRole('dialog',{name:'新增通知渠道'})
  await dialog.getByLabel('渠道名称 *').fill('事故值班群')
  await dialog.getByLabel('渠道 ID').fill('incident-room')
  await dialog.getByLabel('用途说明').fill('严重事故和恢复')
  await dialog.getByLabel('Webhook *').fill('https://oapi.dingtalk.com/robot/send?access_token=secret')
  await dialog.getByRole('button',{name:'保存渠道'}).click()
  await expect.poll(()=>mock.bulkActions.at(-1)).toBe('notification-channel:create')
  const card=page.locator('.notification-channel-card').filter({hasText:'事故值班群'})
  await expect(card).toContainText('https://oapi.dingtalk.com/***')
  await page.getByLabel('新事故目标渠道').selectOption('incident-room')
  await page.getByLabel('事故恢复目标渠道').selectOption('incident-room')
  await page.getByRole('button',{name:'保存路由'}).click()
  await expect.poll(()=>mock.bulkActions.at(-1)).toBe('notification-routes:save')
  await card.getByRole('button',{name:'测试'}).click()
  await expect.poll(()=>mock.bulkActions.at(-1)).toBe('notification-channel:test')
  await page.getByRole('button',{name:/界面主题/}).click();await page.getByRole('menuitemradio',{name:/极昼控制台/}).click()
  await expect(card).not.toHaveCSS('background-color','rgb(7, 23, 34)')
  await page.setViewportSize({width:375,height:760});await expectNoHorizontalOverflow(page)
  await card.getByRole('button',{name:'删除'}).click()
  await confirmDialog(page, '删除渠道')
  await expect.poll(()=>mock.bulkActions.at(-1)).toBe('notification-channel:delete')
  await expect(page.getByLabel('新事故目标渠道')).toHaveValue('')
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
