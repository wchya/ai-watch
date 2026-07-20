import { expect, test } from '@playwright/test'
import { installApiMock } from './api-mock'

const shots = [
  ['/', 'dashboard.png', '让每一次连接，都有迹可循。'],
  ['/providers', 'providers.png', '供应商配置'],
  ['/validation/comparisons', 'validation.png', '对比历史'],
  ['/automation/failover', 'automation.png', '先验证备用线路，再做切换决定。'],
  ['/stability/reliability', 'stability.png', '比较每条线路的真实稳定性。'],
  ['/settings/diagnostics', 'settings.png', '系统诊断'],
] as const

test('generate README screenshots', async ({ page }) => {
  test.setTimeout(120_000)
  await installApiMock(page)
  await page.setViewportSize({ width: 1440, height: 960 })
  await page.goto('/')
  await page.getByRole('button', { name: /界面主题/ }).click()
  await page.getByRole('menuitemradio', { name: /极昼控制台/ }).click()
  await expect(page.locator('.app-shell')).toHaveClass(/theme-arctic-daylight/)

  for (const [path, file, heading] of shots) {
    await page.goto(path)
    await expect(page.getByText(heading, { exact: false }).first()).toBeVisible()
    await page.screenshot({ path: `../docs/images/${file}`, fullPage: true, animations: 'disabled' })
  }
})
