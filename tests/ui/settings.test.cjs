const {test, before, after} = require('node:test')
const assert = require('node:assert/strict')
const {chromium} = require('playwright')
const {spawn, execFileSync} = require('node:child_process')
const {mkdtempSync, rmSync, readFileSync, mkdirSync} = require('node:fs')
const {tmpdir} = require('node:os')
const {join, resolve} = require('node:path')
const http = require('node:http')

const root = resolve(__dirname, '../..')
let directory, fixture, serverProcess, browser, base, fixtureBase, feeds, items
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
async function until(fn) {
  for (let i = 0; i < 100; i++) {
    if (await fn()) return
    await sleep(100)
  }
  throw new Error('Timed out waiting for fixture server')
}
async function api(path, method = 'GET', data) {
  const response = await fetch(base + path, {method, headers: {'x-requested-by': 'yarr', 'content-type': 'application/json'}, body: data === undefined ? undefined : JSON.stringify(data)})
  assert.equal(response.ok, true, `${method} ${path}: ${response.status}`)
  return response
}
async function page(width = 1440) {
  const context = await browser.newContext({viewport: {width, height: 900}})
  // Font providers are not part of the offline regression suite.
  await context.route('https://fontsapi.zeoseven.com/**', route => route.abort())
  const p = await context.newPage()
  p.errors = []
  p.on('pageerror', error => p.errors.push(error.message))
  await p.goto(base)
  await p.waitForFunction(() => window.vm && vm.feeds.length >= 2 && !vm.loading.items)
  return p
}
async function close(p) {
  assert.deepEqual(p.errors, [], 'browser runtime errors')
  await p.context().close()
}
async function system(p) {
  await p.locator('#col-feed-list button[aria-label="设置"]').click()
  await p.locator('#settings-heading').waitFor()
}
async function feedSettings(p, id = feeds[0].id) {
  await p.evaluate(id => { vm.feedSelected = 'feed:' + id }, id)
  await p.waitForFunction(id => vm.current.feed.id === id && !vm.loading.items, id)
  await p.locator('button.item-list-selection-settings').click()
  await p.locator('#settings-heading').waitFor()
}

before(async () => {
  directory = mkdtempSync(join(tmpdir(), 'yarr-ui-'))
  fixture = http.createServer((req, res) => {
    if (req.url === '/icon.svg') {
      res.setHeader('content-type', 'image/svg+xml')
      return res.end(readFileSync(join(root, 'src/assets/graphicarts/rss.svg')))
    }
    if (req.url.startsWith('/article/')) {
      res.setHeader('content-type', 'text/html')
      return res.end('<html><head><title>Reader fixture</title></head><body><article>' + '<p>Readable article content for settings navigation and scroll restoration.</p>'.repeat(100) + '</article></body></html>')
    }
    res.setHeader('content-type', 'application/rss+xml')
    const prefix = req.url.includes('second') ? 'second' : 'first'
    res.end(`<?xml version="1.0"?><rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>${prefix} source</title><link>${fixtureBase}</link><description>UI fixtures</description><image><url>${fixtureBase}/icon.svg</url><title>RSS</title><link>${fixtureBase}</link></image>` +
      Array.from({length: 8}, (_, index) => `<item><guid>${prefix}-${index}</guid><title>Article ${prefix} ${index}</title>${index === 1 ? '' : `<dc:creator><![CDATA[${['Writer Name', '', prefix + ' source', 'source', prefix + ' source extra', prefix.toUpperCase() + ' SOURCE', '很长的作者名称'.repeat(30), '<img src=x onerror=alert(1)>'][index]}]]></dc:creator>`}<link>${fixtureBase}/article/${prefix}/${index}</link><description><![CDATA[<img src="${fixtureBase}/icon.svg" alt="RSS"><p>${'Article body for navigation testing. '.repeat(300)}</p>]]></description></item>`).join('') + '</channel></rss>')
  })
  await new Promise(resolve => fixture.listen(0, '127.0.0.1', resolve))
  fixtureBase = 'http://127.0.0.1:' + fixture.address().port
  const reservation = http.createServer()
  await new Promise(resolve => reservation.listen(0, '127.0.0.1', resolve))
  const port = reservation.address().port
  await new Promise(resolve => reservation.close(resolve))
  base = 'http://127.0.0.1:' + port
  const binary = join(directory, 'yarr')
  const tags = 'sqlite_foreign_keys sqlite_json' + (process.env.YARR_UI_EMBEDDED ? '' : ' debug')
  execFileSync('go', ['build', '-tags', tags, '-o', binary, './cmd/yarr'], {cwd: root})
  serverProcess = spawn(binary, ['-db', join(directory, 'test.db'), '-addr', '127.0.0.1:' + port], {cwd: root, stdio: 'ignore'})
  await until(async () => { try { return (await fetch(base)).ok } catch { return false } })
  feeds = []
  for (const name of ['first', 'second']) {
    const result = await (await api('/api/feeds', 'POST', {url: fixtureBase + '/' + name + '.xml'})).json()
    assert.equal(result.status, 'success')
    feeds.push(result.feed)
    await api('/api/feeds/' + result.feed.id + '/refresh', 'POST')
  }
  await until(async () => {
    items = (await (await api('/api/items')).json()).list
    return items.length >= 16
  })
  browser = await chromium.launch({headless: true, channel: process.env.PLAYWRIGHT_CHANNEL || 'chrome'})
})

after(async () => {
  if (browser) await browser.close()
  if (serverProcess && serverProcess.exitCode === null) {
    const exited = new Promise(resolve => serverProcess.once('exit', resolve))
    serverProcess.kill('SIGTERM')
    await exited
  }
  if (fixture) await new Promise(resolve => fixture.close(resolve))
  if (directory) rmSync(directory, {recursive: true, force: true})
})

test('settings preserve the reading DOM, scroll position and keyboard isolation', async () => {
  const p = await page()
  const first = p.locator('#item-list-scroll .selectgroup-label').first()
  await first.click()
  await p.waitForFunction(() => vm.itemSelectedDetails)
  const id = await p.evaluate(() => vm.itemSelected)
  await p.locator('.content').evaluate(el => { el.scrollTop = 350; el.dataset.retained = 'yes' })
  let articleRequests = 0
  p.on('request', request => { if (request.url().endsWith('/api/items/' + id)) articleRequests++ })
  await system(p)
  assert.equal(await p.locator('[role="dialog"]').count(), 0)
  assert.equal(await p.locator('.content').isVisible(), false)
  assert.equal(await p.locator('#col-item > .toolbar').count(), 0)
  for (const key of ['j', 'k', 'r', 's', 'R', '1']) await p.keyboard.press(key)
  assert.equal(await p.evaluate(() => vm.itemSelected), id)
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  assert.equal(await p.locator('.content').evaluate(el => el.scrollTop), 350)
  assert.equal(await p.locator('.content').getAttribute('data-retained'), 'yes')
  assert.equal(articleRequests, 0)
  await system(p)
  await first.click()
  await p.waitForFunction(() => !vm.settings)
  assert.equal(await p.evaluate(() => vm.itemSelected), id)
  await close(p)
})

test('inline drafts survive navigation; errors and late replies stay with their object', async () => {
  const p = await page()
  await feedSettings(p)
  await p.getByRole('button', {name: '编辑标题', exact: true}).click()
  const title = p.getByRole('textbox', {name: '标题', exact: true})
  await title.fill('Retained draft')
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  await feedSettings(p)
  assert.equal(await title.inputValue(), 'Retained draft')
  await p.route('**/api/feeds/' + feeds[0].id, route => route.request().method() === 'PUT' ? route.fulfill({status: 400}) : route.continue())
  await title.press('Enter')
  await p.locator('.setting-field-error').waitFor()
  assert.equal(await title.inputValue(), 'Retained draft')
  assert.equal(await p.evaluate(() => vm.settingsFeed.title), feeds[0].title)
  await p.unroute('**/api/feeds/' + feeds[0].id)
  let release, started
  const requested = new Promise(resolve => { started = resolve })
  const delayed = new Promise(resolve => { release = resolve })
  await p.route('**/api/feeds/' + feeds[0].id, async route => {
    if (route.request().method() !== 'PUT') return route.continue()
    started()
    await delayed
    await route.continue()
  })
  await title.press('Enter')
  await requested
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  await feedSettings(p, feeds[1].id)
  release()
  await p.waitForFunction(id => vm.feeds.find(feed => feed.id === id).title === 'Retained draft', feeds[0].id)
  assert.equal(await p.evaluate(() => vm.settingsFeed.id), feeds[1].id)
  assert.equal(await p.evaluate(() => vm.settingsFeed.title), feeds[1].title)
  await close(p)
})

test('failed global saves retain the saved value; successful saves persist', async () => {
  const p = await page()
  await system(p)
  await p.route('**/api/settings', route => route.request().method() === 'PUT' ? route.fulfill({status: 500}) : route.continue())
  await p.locator('.feed-sort-picker').getByRole('button', {name: '名称'}).click()
  await p.locator('.settings-notice').waitFor()
  assert.equal(await p.evaluate(() => vm.feedSort), 'time')
  await p.unroute('**/api/settings')
  await p.locator('.feed-sort-picker').getByRole('button', {name: '名称'}).click()
  await p.waitForFunction(() => vm.feedSort === 'name')
  assert.equal((await (await api('/api/settings')).json()).feed_sort, 'name')
  await api('/api/settings', 'PUT', {feed_sort: 'time'})
  await close(p)
})

test('article media stays responsive while only ranking cover previews are cropped', async () => {
  const p = await page(390)
  const portrait = 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="200" height="400"><rect width="200" height="400" fill="green"/></svg>')
  const player = 'https://www.bilibili.com/blackboard/html5mobileplayer.html?aid=20'
  const content = `<img id="plain-image" src="${portrait}">
    <iframe id="plain-video" width="640" height="360" src="${player}" frameborder="0"></iframe>
    <article class="bilibili-ranking-card">
      <a class="bilibili-ranking-link"><img id="ranking-cover" class="bilibili-ranking-image" src="${portrait}"></a>
      <div class="bilibili-ranking-body"><details open><summary>Ranking entry</summary>
        <div class="bilibili-ranking-content">
          <img id="expanded-image" class="bilibili-ranking-image" src="${portrait}">
          <iframe id="expanded-video" width="640" height="360" src="${player}" frameborder="0"></iframe>
        </div>
      </details></div>
    </article>`
  await p.route(player, route => route.fulfill({contentType: 'text/html', body: '<html><body>Video player fixture</body></html>'}))
  await p.route('**/api/items/' + items[0].id, route => route.fulfill({json: {...items[0], content}}))
  await p.evaluate(id => { vm.itemSelected = id }, items[0].id)
  await p.waitForFunction(() => vm.itemSelectedDetails && vm.layerSlide === '' && Array.from(document.querySelectorAll('.content img')).every(img => img.complete && img.naturalWidth))
  for (const width of [390, 1440]) {
    await p.setViewportSize({width, height: 900})
    await p.waitForFunction(width => vm.mobileLayout === (width < 768), width)
    const metrics = await p.locator('#plain-image, #plain-video, #ranking-cover, #expanded-image, #expanded-video').evaluateAll(elements => elements.map(el => {
      const rect = el.getBoundingClientRect()
      return {id: el.id, ratio: rect.width / rect.height, width: rect.width, available: el.parentElement.clientWidth, border: getComputedStyle(el).borderWidth}
    }))
    for (const media of metrics) {
      const ratio = ['plain-image', 'expanded-image'].includes(media.id) ? 1 / 2 : 16 / 9
      assert.ok(Math.abs(media.ratio - ratio) < .02, `${width}px ${media.id}: expected ${ratio}, got ${media.ratio}`)
      assert.ok(media.width <= media.available + 1, `${media.id} overflows its container`)
      if (media.id.endsWith('video')) assert.equal(media.border, '0px')
    }
  }
  await close(p)
})

test('Noto Sans CJK replaces Maple and preserves the font preference across reloads', async () => {
  const p = await page()
  await p.evaluate(() => localStorage.setItem('yarr.themeFont.v1', JSON.stringify('maple-mono-nf-cn')))
  await p.reload()
  await p.waitForFunction(() => window.vm)
  assert.equal(await p.evaluate(() => vm.theme.font), 'noto-sans-cjk')
  assert.equal(await p.locator('body').evaluate(el => getComputedStyle(el).fontFamily.split(',')[0]), '"Noto Sans CJK"')
  assert.equal(await p.locator('link[href="https://fontsapi.zeoseven.com/69/main/result.css"]').count(), 1)
  assert.equal(await p.locator('link[href="https://fontsapi.zeoseven.com/442/main/result.css"]').count(), 0)
  await system(p)
  assert.equal(await p.getByRole('button', {name: 'Maple Mono NF-CN', exact: true}).count(), 0)
  assert.equal(await p.getByRole('button', {name: '思源黑体', exact: true}).getAttribute('aria-pressed'), 'true')
  await p.getByRole('button', {name: '霞鹜文楷', exact: true}).click()
  assert.equal(await p.evaluate(() => vm.theme.font), 'lxgw-wenkai')
  await p.getByRole('button', {name: '思源黑体', exact: true}).click()
  assert.equal(await p.evaluate(() => JSON.parse(localStorage.getItem('yarr.themeFont.v1'))), 'noto-sans-cjk')
  await p.reload()
  await p.waitForFunction(() => window.vm)
  assert.equal(await p.evaluate(() => vm.theme.font), 'noto-sans-cjk')
  assert.equal(await p.locator('body').evaluate(el => getComputedStyle(el).fontFamily.split(',')[0]), '"Noto Sans CJK"')
  await close(p)
})

test('GenSenRounded2 TC is available and its selection persists on desktop and mobile', async () => {
  const p = await page()
  for (const width of [1440, 390]) {
    await p.setViewportSize({width, height: 900})
    await p.waitForFunction(width => vm.mobileLayout === (width < 768), width)
    await p.evaluate(() => vm.showFeedList())
    await p.waitForFunction(() => vm.feedSelected === null && vm.layerSlide === '')
    await system(p)
    for (const name of ['霞鹜文楷', '思源黑体', '源泉圆体']) {
      assert.equal(await p.getByRole('button', {name, exact: true}).isVisible(), true)
    }
    const buttons = await p.locator('.font-picker > button').evaluateAll(elements => elements.map(el => {
      const rect = el.getBoundingClientRect()
      return {top: rect.top, width: rect.width}
    }))
    assert.equal(buttons.length, 3)
    for (const button of buttons) {
      assert.ok(Math.abs(button.top - buttons[0].top) < 1, `${width}px font options must share a row`)
      assert.ok(Math.abs(button.width - buttons[0].width) < 1, `${width}px font options must have equal widths`)
    }
    await p.getByRole('button', {name: '源泉圆体', exact: true}).click()
    assert.equal(await p.evaluate(() => vm.theme.font), 'gensen-rounded2-tc')
    assert.equal(await p.locator('body').evaluate(el => getComputedStyle(el).fontFamily.split(',')[0]), '"GenSenRounded2 TC"')
    assert.equal(await p.locator('body').evaluate(el => getComputedStyle(el).fontWeight), '400')
    assert.equal(await p.evaluate(() => JSON.parse(localStorage.getItem('yarr.themeFont.v1'))), 'gensen-rounded2-tc')
    assert.equal(await p.locator('link[href="https://fontsapi.zeoseven.com/303/main/result.css"]').count(), 1)
    await p.reload()
    await p.waitForFunction(() => window.vm)
    assert.equal(await p.evaluate(() => vm.theme.font), 'gensen-rounded2-tc')
    assert.equal(await p.locator('body').evaluate(el => getComputedStyle(el).fontFamily.split(',')[0]), '"GenSenRounded2 TC"')
  }
  await close(p)
})

test('browser preference failures retain the previous theme and font size', async () => {
  const p = await page()
  await system(p)
  await p.evaluate(() => { Storage.prototype.setItem = function() { throw new Error('Storage blocked') } })
  await p.getByRole('button', {name: '夜间', exact: true}).click()
  await p.waitForFunction(() => vm.settingsNotice.includes('存储权限'))
  assert.equal(await p.evaluate(() => vm.theme.name), 'light')
  await p.locator('#settings-font-size').fill('20')
  await p.locator('#settings-font-size').press('Tab')
  assert.equal(await p.evaluate(() => vm.appFontSize), 15)
  assert.equal(await p.locator('#settings-font-size').inputValue(), '15')
  await close(p)
})

test('mobile browser Back restores settings parents then the original reading layer', async () => {
  const p = await page(390)
  await p.evaluate(() => vm.showFeedList())
  await p.waitForFunction(() => vm.feedSelected === null)
  await system(p)
  await p.getByRole('button', {name: '基础链接列表', exact: true}).click()
  await p.locator('#rsshub-base-url').fill('https://example.com\n#https://disabled.example.com')
  await p.goBack()
  await p.waitForFunction(() => vm.settings === 'menu')
  await p.getByRole('button', {name: '基础链接列表', exact: true}).click()
  assert.match(await p.locator('#rsshub-base-url').inputValue(), /disabled/)
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  await p.waitForFunction(() => vm.settings === '')
  assert.equal(await p.evaluate(() => vm.feedSelected), null)
  assert.equal(await p.locator('#col-feed-list').isVisible(), true)
  assert.equal(new URL(p.url()).pathname, '/')
  await close(p)
})

test('mobile browser Back does not replay a layer animation after native navigation', async () => {
  const p = await page(390)
  await p.evaluate(id => { vm.feedSelected = 'feed:' + id }, feeds[0].id)
  await p.waitForFunction(() => !vm.loading.items && vm.layerSlide === '')
  await p.locator('#item-list-scroll .selectgroup-label').first().click()
  await p.waitForFunction(() => vm.itemSelectedDetails && vm.layerSlide === '')
  await p.evaluate(() => {
    window.backAnimations = []
    document.getElementById('app').addEventListener('animationstart', event => {
      if (event.animationName === 'layer-in-back') window.backAnimations.push(event.target.id)
    })
  })
  for (const layer of ['items', 'feeds']) {
    await p.goBack()
    await p.waitForFunction(layer => vm.currentNavigationLayer() === layer, layer)
    const result = await p.evaluate(() => new Promise(resolve => {
      requestAnimationFrame(() => requestAnimationFrame(() => {
        const column = document.getElementById(vm.feedSelected === null ? 'col-feed-list' : 'col-item-list')
        const style = getComputedStyle(column)
        resolve({animations: window.backAnimations, opacity: style.opacity, transform: style.transform})
      }))
    }))
    assert.deepEqual(result, {animations: [], opacity: '1', transform: 'none'}, layer + ' must stay visible after browser Back')
  }
  await close(p)
})

test('mobile toolbar Back retains its animation and browser Back cancels an active forward slide', async () => {
  const p = await page(390)
  await p.evaluate(id => { vm.feedSelected = 'feed:' + id }, feeds[0].id)
  await p.waitForFunction(() => !vm.loading.items && vm.layerSlide === '')
  await p.locator('#item-list-scroll .selectgroup-label').first().click()
  await p.waitForFunction(() => vm.itemSelectedDetails && vm.layerSlide === '')
  await p.getByRole('button', {name: '关闭文章', exact: true}).click()
  await p.waitForFunction(() => vm.currentNavigationLayer() === 'items' && vm.layerSlide === 'back')
  assert.equal(await p.locator('#col-item-list').evaluate(el => getComputedStyle(el).animationName), 'layer-in-back')
  await p.waitForFunction(() => vm.layerSlide === '')

  await p.locator('#item-list-scroll .selectgroup-label').first().click()
  await p.waitForFunction(() => vm.currentNavigationLayer() === 'item' && vm.layerSlide === 'forward')
  await p.goBack()
  await p.waitForFunction(() => vm.currentNavigationLayer() === 'items')
  const result = await p.evaluate(() => new Promise(resolve => {
    requestAnimationFrame(() => requestAnimationFrame(() => {
      const style = getComputedStyle(document.getElementById('col-item-list'))
      resolve({opacity: style.opacity, transform: style.transform, animation: style.animationName})
    }))
  }))
  assert.deepEqual(result, {opacity: '1', transform: 'none', animation: 'none'})

  await p.getByRole('button', {name: '显示订阅源', exact: true}).click()
  await p.waitForFunction(() => vm.currentNavigationLayer() === 'feeds' && vm.layerSlide === 'back')
  assert.equal(await p.locator('#col-feed-list').evaluate(el => getComputedStyle(el).animationName), 'layer-in-back')
  await close(p)
})

test('all settings pages fit desktop, tablet and phone widths in every theme', async () => {
  const p = await page()
  const screenshots = process.env.YARR_UI_SCREENSHOTS
  if (screenshots) mkdirSync(screenshots, {recursive: true})
  for (const width of [1440, 1024, 768, 390, 320]) {
    await p.setViewportSize({width, height: 900})
    for (const theme of ['light', 'sepia', 'night']) {
      await p.evaluate(theme => { vm.theme.name = theme }, theme)
      for (const name of ['menu', 'feed', 'folder', 'allfeeds', 'create', 'deletefeeds', 'auth', 'bilibili', 'rsshub', 'rsshubdetails', 'shortcuts']) {
        await p.evaluate(({name, id}) => {
          vm.finishClosingSettings()
          if (name === 'feed') vm.showFeedSettings(vm.feeds.find(feed => feed.id === id))
          else if (name === 'folder') {
            if (!vm.folders.some(folder => folder.id === 9999)) vm.folders.push({id: 9999, title: 'Folder fixture'})
            vm.showFolderSettings(vm.folders.find(folder => folder.id === 9999))
          }
          else vm.showSettings(name)
        }, {name, id: feeds[0].id})
        await p.locator('.settings-workspace').waitFor()
        if (['menu', 'feed', 'folder', 'allfeeds', 'deletefeeds'].includes(name)) {
          await p.locator('.settings-scroll > .settings-dialog:not([class*="settings-page-"])').waitFor()
        }
        const sizes = await p.locator('.settings-workspace').evaluate(el => ({width: el.clientWidth, scroll: el.scrollWidth, body: document.documentElement.scrollWidth, viewport: innerWidth}))
        assert.ok(sizes.width > 200, `${width}/${theme}/${name}: nonblank settings`)
        assert.ok(sizes.scroll <= sizes.width + 1, `${width}/${theme}/${name}: horizontal overflow ${JSON.stringify(sizes)}`)
        assert.ok(sizes.body <= sizes.viewport + 1, `${width}/${theme}/${name}: page overflow`)
        if (width <= 390 && ['folder', 'allfeeds'].includes(name)) {
          assert.equal(await p.locator('.settings-bulk-pair').first().evaluate(el => getComputedStyle(el).gridTemplateColumns.split(' ').length), 1)
        }
        assert.equal(await p.locator('[role="dialog"]').count(), 0)
        if (screenshots && ['menu', 'feed', 'folder', 'allfeeds'].includes(name)) await p.screenshot({path: join(screenshots, `${width}-${theme}-${name}.png`)})
      }
    }
  }
  await p.emulateMedia({reducedMotion: 'reduce'})
  await p.evaluate(() => vm.showSettings('allfeeds'))
  await p.locator('.settings-bulk button').first().waitFor()
  assert.equal(await p.locator('.settings-bulk button').first().evaluate(el => getComputedStyle(el).transitionDuration), '0s')
  await close(p)
})

test('confirmation can be cancelled by Escape and traps keyboard focus', async () => {
  const p = await page()
  await feedSettings(p)
  await p.locator('.settings-workspace').getByRole('button', {name: '删除', exact: true}).click()
  await p.getByRole('dialog').waitFor()
  assert.equal(await p.evaluate(() => document.activeElement.textContent.trim()), '取消')
  await p.keyboard.press('Shift+Tab')
  assert.equal(await p.evaluate(() => document.activeElement.textContent.trim()), '确定')
  await p.keyboard.press('Escape')
  assert.equal(await p.getByRole('dialog').count(), 0)
  assert.ok((await (await api('/api/feeds')).json()).some(feed => feed.id === feeds[0].id))
  await close(p)
})

test('templates are private and split assets have cache versions', async () => {
  for (const name of ['index.html', 'settings.html']) {
    assert.equal((await fetch(base + '/static/' + name)).status, 404)
  }
  const html = await (await fetch(base)).text()
  for (const file of ['settings.js', 'navigation.js', 'components.js', 'settings.css']) {
    assert.ok(html.includes(file + '?v='))
  }
  const p = await page()
  await close(p)
})

test('text cancellation, content modes and nested new-folder editing work inline', async () => {
  const p = await page()
  await feedSettings(p)
  await p.getByRole('button', {name: '编辑订阅链接', exact: true}).click()
  await p.getByRole('textbox', {name: '订阅链接', exact: true}).fill('discarded')
  await p.getByRole('textbox', {name: '订阅链接', exact: true}).press('Escape')
  assert.equal(await p.getByRole('textbox', {name: '订阅链接', exact: true}).count(), 0)
  await p.getByRole('button', {name: '正文', exact: true}).click()
  await p.getByRole('button', {name: '编辑正文选择器', exact: true}).waitFor()
  await p.getByRole('button', {name: '编辑正文选择器', exact: true}).click()
  await p.getByRole('textbox', {name: '正文选择器', exact: true}).fill('article')
  await p.getByRole('textbox', {name: '正文选择器', exact: true}).press('Enter')
  await p.waitForFunction(() => vm.settingsFeed.content_selector === 'article')
  await p.getByRole('button', {name: '普通', exact: true}).click()
  await p.waitForFunction(() => vm.settingsFeed.content_mode === 'normal')
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  await system(p)
  await p.getByRole('button', {name: '新订阅源', exact: true}).click()
  await p.getByRole('button', {name: '编辑新文件夹', exact: true}).click()
  await p.getByRole('textbox', {name: '新文件夹', exact: true}).fill('Created inline')
  await p.getByRole('textbox', {name: '新文件夹', exact: true}).press('Enter')
  await p.waitForFunction(() => vm.folders.some(folder => folder.title === 'Created inline') && vm.feedNewFolderId)
  assert.equal(await p.evaluate(() => vm.loading.newfeed), false)
  assert.equal(await p.locator('[role="dialog"]').count(), 0)
  await close(p)
})

test('RSSHub saves, OPML imports and backups leave settings open with feedback', async () => {
  const p = await page()
  await system(p)
  await p.getByRole('button', {name: '基础链接列表', exact: true}).click()
  await p.locator('#rsshub-base-url').fill(fixtureBase)
  await p.getByRole('button', {name: '保存', exact: true}).click()
  await p.waitForFunction(url => vm.rsshubBaseUrl === url, fixtureBase)
  assert.equal(await p.evaluate(() => vm.settings), 'rsshub')
  await p.getByRole('button', {name: '返回', exact: true}).click()
  await p.waitForFunction(() => vm.settings === 'menu')
  const opml = await (await api('/opml/export')).text()
  await p.locator('#opml-import').setInputFiles({name: 'test.opml', mimeType: 'text/xml', buffer: Buffer.from(opml)})
  await p.waitForFunction(() => vm.settingsNotice === '导入完成。')
  await p.getByRole('button', {name: '备份数据', exact: true}).click()
  await p.waitForFunction(() => vm.settingsNotice.includes('storage.db'))
  assert.match(await p.locator('.settings-notice').textContent(), /subscriptions.opml/)
  assert.equal(await p.evaluate(() => vm.settings), 'menu')
  assert.equal(await p.locator('[role="dialog"]').count(), 0)
  await close(p)
})

test('authentication can be saved and disabled without reloading the reading page', async () => {
  const p = await page()
  await system(p)
  await p.getByRole('button', {name: '访问认证', exact: true}).click()
  await p.waitForFunction(() => !vm.settingsPending['auth-load'])
  await p.getByRole('button', {name: '启用', exact: true}).click()
  await p.locator('#auth-username').fill('ui-test')
  await p.locator('#auth-password').fill('temporary-ui-password')
  await p.getByRole('button', {name: '保存', exact: true}).click()
  await p.waitForFunction(() => vm.authenticated && !vm.settingsPending.auth)
  assert.equal(await p.locator('#auth-password').inputValue(), '')
  assert.equal(await p.evaluate(() => vm.settings), 'auth')
  await p.getByRole('button', {name: '关闭', exact: true}).click()
  await p.getByRole('button', {name: '保存', exact: true}).click()
  await p.getByRole('dialog').getByRole('button', {name: '确定', exact: true}).click()
  await p.waitForFunction(() => !vm.authenticated && !vm.settingsPending.auth)
  assert.equal((await (await api('/api/auth')).json()).enabled, false)
  await close(p)
})

test('partial deletion keeps failed entries and successful deletion preserves unrelated reading', async () => {
  const p = await page()
  await p.locator('#item-list-scroll .selectgroup-label').first().click()
  await p.waitForFunction(() => vm.itemSelectedDetails)
  const readingId = await p.evaluate(() => vm.itemSelected)
  const newIds = []
  for (const name of ['delete-a', 'delete-b']) {
    const result = await (await api('/api/feeds', 'POST', {url: fixtureBase + '/' + name + '.xml'})).json()
    newIds.push(result.feed.id)
  }
  await p.evaluate(() => vm.refreshFeeds())
  await system(p)
  await p.getByRole('button', {name: '删除订阅源', exact: true}).click()
  for (const id of newIds) await p.locator(`.feed-delete-row input[value="${id}"]`).check()
  await p.route('**/api/feeds/' + newIds[1], route => route.request().method() === 'DELETE' ? route.fulfill({status: 500}) : route.continue())
  await p.getByRole('button', {name: '删除', exact: true}).click()
  await p.getByRole('dialog').getByRole('button', {name: '确定', exact: true}).click()
  await p.waitForFunction(() => !vm.loading.deletefeeds && vm.settingsNotice.includes('部分'))
  assert.deepEqual(await p.evaluate(() => vm.feedDeleteSelectedIds), [newIds[1]])
  assert.equal(await p.evaluate(() => vm.itemSelected), readingId)
  await p.unroute('**/api/feeds/' + newIds[1])
  await p.getByRole('button', {name: '删除', exact: true}).click()
  await p.getByRole('dialog').getByRole('button', {name: '确定', exact: true}).click()
  await p.waitForFunction(() => vm.settings === 'menu')
  assert.equal(await p.evaluate(() => vm.itemSelected), readingId)
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  assert.equal(await p.locator('.content').isVisible(), true)
  await close(p)
})

test('a removed settings target returns to the system page without affecting the article', async () => {
  const p = await page()
  await p.locator('#item-list-scroll .selectgroup-label').first().click()
  await p.waitForFunction(() => vm.itemSelectedDetails)
  const readingId = await p.evaluate(() => vm.itemSelected)
  const result = await (await api('/api/feeds', 'POST', {url: fixtureBase + '/removed.xml'})).json()
  await p.evaluate(() => vm.refreshFeeds())
  await feedSettings(p, result.feed.id)
  await api('/api/feeds/' + result.feed.id, 'DELETE')
  await p.evaluate(() => vm.refreshFeeds())
  await p.waitForFunction(() => vm.settings === 'menu')
  assert.equal(await p.evaluate(() => vm.itemSelected), readingId)
  assert.match(await p.locator('.settings-notice').textContent(), /已不存在/)
  await close(p)
})

test('article content modes and iframe instances survive a settings round trip', async () => {
  const p = await page()
  // The real crawler intentionally rejects loopback URLs (SSRF protection).
  // Its extraction is covered by Go tests; here exercise the reading UI contract.
  await p.route('**/page?*', route => route.fulfill({json: {content: '<article><p>Extracted content fixture.</p></article>'}}))
  await p.locator('#item-list-scroll .selectgroup-label').first().click()
  await p.waitForFunction(() => vm.itemSelectedDetails)
  await p.locator('#col-item > .toolbar').getByRole('button', {name: '正文', exact: true}).click()
  await p.waitForFunction(() => !vm.loading.readability)
  assert.equal(await p.evaluate(() => vm.itemSelectedReadabilityError), '')
  assert.ok(await p.evaluate(() => vm.itemSelectedReadability))
  const extracted = await p.evaluate(() => vm.itemSelectedReadability)
  await system(p)
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  assert.equal(await p.evaluate(() => vm.itemSelectedContentMode), 'readability')
  assert.equal(await p.evaluate(() => vm.itemSelectedReadability), extracted)
  await p.locator('#col-item > .toolbar').getByRole('button', {name: '嵌入', exact: true}).click()
  await p.locator('.content-embed').waitFor()
  await p.locator('.content-embed').evaluate(el => { el.dataset.retained = 'yes' })
  let embeddedRequests = 0
  p.on('request', request => { if (request.resourceType() === 'document' && request.url().includes('/article/')) embeddedRequests++ })
  await system(p)
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  assert.equal(await p.evaluate(() => vm.itemSelectedContentMode), 'embed')
  assert.equal(await p.locator('.content-embed').getAttribute('data-retained'), 'yes')
  assert.equal(embeddedRequests, 0)
  await p.locator('#col-item > .toolbar').getByRole('button', {name: '普通', exact: true}).click()
  await p.waitForFunction(() => vm.itemSelectedContentMode === 'normal')
  await close(p)
})

test('returning from child settings restores the parent scroll position', async () => {
  const p = await page(390)
  await p.setViewportSize({width: 390, height: 500})
  await p.evaluate(() => vm.showFeedList())
  await p.waitForFunction(() => vm.feedSelected === null)
  await system(p)
  const button = p.getByRole('button', {name: '访问认证', exact: true})
  await button.scrollIntoViewIfNeeded()
  const top = await p.locator('.settings-scroll').evaluate(el => el.scrollTop)
  assert.ok(top > 0)
  await button.click()
  await p.locator('#settings-heading').filter({hasText: '访问认证'}).waitFor()
  await p.getByRole('button', {name: '返回', exact: true}).click()
  await p.waitForFunction(() => vm.settings === 'menu')
  assert.equal(await p.locator('.settings-scroll').evaluate(el => el.scrollTop), top)
  await close(p)
})

test('long settings values and maximum font size remain usable on a small phone', async () => {
  const p = await page(320)
  await p.evaluate(id => {
    const feed = vm.feeds.find(feed => feed.id === id)
    feed.title = 'VeryLongSubscriptionTitle'.repeat(12)
    feed.feed_link = 'https://example.com/' + 'long-path'.repeat(50)
    vm.appFontSize = 30
    vm.showFeedSettings(feed)
  }, feeds[0].id)
  await p.locator('.settings-workspace').waitFor()
  await p.getByRole('button', {name: '编辑标题', exact: true}).click()
  const overflow = await p.locator('.settings-workspace').evaluate(el => Array.from(el.querySelectorAll('button, .settings-dialog-field, .setting-field-actions')).filter(node => node.clientWidth && node.scrollWidth > node.clientWidth + 1).map(node => ({text: node.textContent.trim(), width: node.clientWidth, scroll: node.scrollWidth})))
  assert.deepEqual(overflow, [])
  assert.ok(await p.locator('.settings-workspace').evaluate(el => el.scrollWidth <= el.clientWidth + 1))
  await close(p)
})

test('Bilibili Cookie settings save without revealing or retaining the credential', async () => {
  const p = await page(390)
  await p.evaluate(() => vm.showFeedList())
  await p.waitForFunction(() => vm.feedSelected === null)
  await system(p)
  await p.getByRole('button', {name: 'Bilibili 关注视频', exact: true}).click()
  await p.waitForFunction(() => !vm.settingsPending['bilibili-load'])
  await p.locator('#bilibili-cookie').fill('SESSDATA=ui-private-fixture; DedeUserID=123')
  await p.getByRole('button', {name: '保存', exact: true}).click()
  await p.waitForFunction(() => vm.bilibiliConfigured && !vm.settingsPending.bilibili)
  assert.equal(await p.locator('#bilibili-cookie').inputValue(), '')
  assert.deepEqual(await (await api('/api/bilibili')).json(), {configured: true})
  assert.equal(await p.evaluate(() => JSON.stringify(app.settings).includes('ui-private-fixture')), false)
  assert.equal(await p.evaluate(() => JSON.stringify(localStorage).includes('ui-private-fixture')), false)
  await p.locator('#bilibili-cookie').fill('unsaved-secret')
  await p.goBack()
  await p.waitForFunction(() => vm.settings === 'menu')
  assert.equal(await p.evaluate(() => vm.bilibiliCookieDraft), '')
  await p.getByRole('button', {name: 'Bilibili 关注视频', exact: true}).click()
  await p.waitForFunction(() => !vm.settingsPending['bilibili-load'])
  assert.equal(await p.locator('#bilibili-cookie').inputValue(), '')
  await p.getByRole('button', {name: '清空 Cookie', exact: true}).click()
  await p.waitForFunction(() => !vm.bilibiliConfigured && !vm.settingsPending.bilibili)
  assert.deepEqual(await (await api('/api/bilibili')).json(), {configured: false})
  await p.locator('#bilibili-cookie').fill('close-secret')
  await p.getByRole('button', {name: '关闭设置', exact: true}).click()
  await p.waitForFunction(() => vm.settings === '')
  assert.equal(await p.evaluate(() => vm.bilibiliCookieDraft), '')
  await close(p)
})

test('article authors follow source scope and stay visible in details', async () => {
  await api('/api/feeds/' + feeds[0].id, 'PUT', {title: 'first source'})
  const p = await page()
  const articles = (await (await api('/api/items?feed_id=' + feeds[0].id)).json()).list
  const byIndex = index => articles.find(item => item.guid === 'first-' + index)
  const row = index => p.locator(`.selectgroup[data-item-id="${byIndex(index).id}"]`)
  await p.evaluate(() => { vm.feedSelected = ''; vm.filterSelected = '' })
  await p.waitForFunction(() => !vm.loading.items && vm.items.length >= 16)
  for (const layout of ['list', 'card']) {
    await p.evaluate(layout => { vm.articleListLayout = layout; return vm.$nextTick() }, layout)
    assert.equal(await row(0).locator('.item-source-name').textContent(), 'first source')
    assert.equal(await row(0).locator('.item-author-name').textContent(), 'Writer Name')
    assert.equal(await row(0).locator('.author-icon').count(), 1)
    for (const index of [1, 2, 3, 4, 5]) assert.equal(await row(index).locator('.item-author').count(), 0)
    assert.equal(await row(1).locator('.item-source-name').textContent(), 'first source')
  }
  await p.evaluate(id => { vm.feedSelected = 'feed:' + id }, feeds[0].id)
  await p.waitForFunction(id => vm.current.feed.id === id && !vm.loading.items && vm.items.length === 8, feeds[0].id)
  assert.equal(await row(0).locator('.item-source-name').count(), 0)
  assert.equal(await row(0).locator('.item-author-name').textContent(), 'Writer Name')
  assert.equal(await row(1).locator('.item-source-name').textContent(), 'first source')
  assert.equal(await row(2).locator('.item-author-name').textContent(), 'first source')
  assert.equal(await row(7).locator('.item-author-name img').count(), 0)
  assert.equal(await row(7).locator('.item-author-name').textContent(), '<img src=x onerror=alert(1)>')
  await row(2).locator('.selectgroup-label').click()
  await p.waitForFunction(id => vm.itemSelectedDetails && vm.itemSelectedDetails.id === id, byIndex(2).id)
  assert.equal((await p.locator('.content-source-name').textContent()).trim(), 'first source')
  assert.equal(await p.locator('.content-author-name').textContent(), 'first source')
  const alignment = await p.locator('.content-attribution').evaluate(el => {
    const icon = el.querySelector('.author-icon').getBoundingClientRect()
    const author = el.querySelector('.content-author-name').getBoundingClientRect()
    const source = el.querySelector('.content-source-name').getBoundingClientRect()
    return {icon: icon.top + icon.height / 2, author: author.top + author.height / 2, source: source.top + source.height / 2}
  })
  assert.ok(Math.abs(alignment.icon - alignment.author) <= 1, JSON.stringify(alignment))
  assert.ok(Math.abs(alignment.source - alignment.author) <= 1, JSON.stringify(alignment))
  for (const mode of ['normal', 'readability', 'embed']) {
    await p.evaluate(mode => { vm.itemSelectedContentMode = mode; return vm.$nextTick() }, mode)
    assert.equal(await p.locator('.content-author-name').isVisible(), true)
  }
  await p.evaluate(id => { vm.feedSelected = 'feed:' + id }, feeds[1].id)
  await p.waitForFunction(id => vm.current.feed.id === id && !vm.loading.items, feeds[1].id)
  assert.equal((await p.locator('.content-source-name').textContent()).trim(), 'first source')
  assert.equal(await p.locator('.content-author-name').textContent(), 'first source')
  await api('/api/items/' + byIndex(0).id, 'PUT', {status: 'unread', favorite: true})
  for (const filter of ['unread', 'favorite']) {
    await p.evaluate(({id, filter}) => { vm.feedSelected = 'feed:' + id; vm.filterSelected = filter }, {id: feeds[0].id, filter})
    await p.waitForFunction(id => !vm.loading.items && vm.items.some(item => item.id === id), byIndex(0).id)
    assert.equal(await row(0).locator('.item-source-name').count(), 0)
    assert.equal(await row(0).locator('.item-author-name').textContent(), 'Writer Name')
  }
  const folder = await (await api('/api/folders', 'POST', {title: 'Author scope'})).json()
  try {
    await api('/api/feeds/' + feeds[0].id, 'PUT', {folder_id: folder.id})
    await p.evaluate(async id => { await vm.refreshFeeds(); vm.filterSelected = ''; vm.feedSelected = 'folder:' + id }, folder.id)
    await p.waitForFunction(() => vm.current.type === 'folder' && !vm.loading.items && vm.items.length === 8)
    assert.equal(await row(0).locator('.item-source-name').textContent(), 'first source')
    assert.equal(await row(0).locator('.item-author-name').textContent(), 'Writer Name')
  } finally {
    await api('/api/feeds/' + feeds[0].id, 'PUT', {folder_id: null})
    await api('/api/folders/' + folder.id, 'DELETE')
  }
  await close(p)
})

test('long article authors truncate in lists and wrap in details on phones', async () => {
  const p = await page()
  const articles = (await (await api('/api/items?feed_id=' + feeds[0].id)).json()).list
  const long = articles.find(item => item.guid === 'first-6')
  await p.evaluate(() => { vm.filterSelected = ''; vm.feedSelected = ''; vm.itemSelected = null })
  await p.waitForFunction(id => !vm.loading.items && vm.items.some(item => item.id === id), long.id)
  for (const width of [1440, 390, 320]) {
    await p.setViewportSize({width, height: 900})
    await p.evaluate(({width, id}) => { vm.feedSelected = width < 768 ? 'feed:' + id : '' }, {width, id: feeds[0].id})
    await p.waitForFunction(id => !vm.loading.items && vm.items.some(item => item.id === id), long.id)
    for (const theme of ['light', 'sepia', 'night']) for (const layout of ['list', 'card']) {
      await p.evaluate(({theme, layout}) => { vm.theme.name = theme; vm.articleListLayout = layout; return vm.$nextTick() }, {theme, layout})
      const label = p.locator(`.selectgroup[data-item-id="${long.id}"]`)
      await label.scrollIntoViewIfNeeded()
      const name = label.locator('.item-author-name')
      assert.equal(await name.getAttribute('title'), long.author)
      const sizes = await label.evaluate(el => ({width: el.clientWidth, scroll: el.scrollWidth, meta: el.querySelector('.item-list-meta').clientWidth, metaScroll: el.querySelector('.item-list-meta').scrollWidth}))
      assert.ok(sizes.scroll <= sizes.width + 1 && sizes.metaScroll <= sizes.meta + 1, JSON.stringify({width, theme, layout, sizes}))
      assert.equal(await label.locator('.author-icon').isVisible(), true)
      assert.equal(await label.locator('.item-list-meta time').isVisible(), true)
    }
  }
  await p.locator(`.selectgroup[data-item-id="${long.id}"] .selectgroup-label`).click()
  await p.waitForFunction(id => vm.itemSelectedDetails && vm.itemSelectedDetails.id === id, long.id)
  assert.equal(await p.locator('.content-author-name').textContent(), long.author)
  const detail = await p.locator('.content-attribution').evaluate(el => ({width: el.clientWidth, scroll: el.scrollWidth}))
  assert.ok(detail.scroll <= detail.width + 1, JSON.stringify(detail))
  await close(p)
})

test('mobile single-source authors include the source and react to viewport changes', async () => {
  await api('/api/feeds/' + feeds[0].id, 'PUT', {title: 'first source'})
  const p = await page()
  const articles = (await (await api('/api/items?feed_id=' + feeds[0].id)).json()).list
  const byIndex = index => articles.find(item => item.guid === 'first-' + index)
  const row = index => p.locator(`.selectgroup[data-item-id="${byIndex(index).id}"]`)
  await api('/api/items/' + byIndex(0).id, 'PUT', {status: 'unread', favorite: true})
  await p.evaluate(id => { vm.feedSelected = 'feed:' + id; vm.itemSelected = null; return vm.$nextTick() }, feeds[0].id)
  for (const width of [1440, 768, 767, 390, 768]) {
    await p.setViewportSize({width, height: 900})
    await p.waitForFunction(mobile => vm.mobileLayout === mobile, width < 768)
    for (const layout of ['list', 'card']) for (const filter of ['', 'unread', 'favorite']) {
      await p.evaluate(({layout, filter}) => { vm.articleListLayout = layout; vm.filterSelected = filter; return vm.$nextTick() }, {layout, filter})
      await p.waitForFunction(id => !vm.loading.items && vm.items.some(item => item.id === id), byIndex(0).id)
      assert.equal(await row(0).locator('.item-source-name').count(), width < 768 ? 1 : 0, JSON.stringify({width, layout, filter}))
      assert.equal(await row(0).locator('.item-author-name').textContent(), 'Writer Name')
      if (filter === '') {
        assert.equal(await row(1).locator('.item-source-name').textContent(), 'first source')
        for (const index of [2, 3, 4, 5]) {
          assert.equal(await row(index).locator('.item-author').count(), width < 768 ? 0 : 1)
          if (width < 768) assert.equal(await row(index).locator('.item-source-name').textContent(), 'first source')
        }
      }
    }
  }
  await p.setViewportSize({width: 390, height: 900})
  await row(0).locator('.selectgroup-label').click()
  await p.waitForFunction(id => vm.itemSelectedDetails && vm.itemSelectedDetails.id === id, byIndex(0).id)
  assert.equal((await p.locator('.content-source-name').textContent()).trim(), 'first source')
  assert.equal(await p.locator('.content-author-name').textContent(), 'Writer Name')
  await close(p)
})

test('read retention settings validate save persist and clear deleted selections', async () => {
  await api('/api/settings', 'PUT', {read_items_keep_limit: 1000})
  const result = await (await api('/api/feeds', 'POST', {url: fixtureBase + '/retention.xml'})).json()
  const id = result.feed.id
  const p = await page()
  try {
    await api('/api/feeds/' + id + '/refresh', 'POST')
    await until(async () => (await (await api('/api/items?feed_id=' + id)).json()).list.length === 8)
    await api('/api/items?feed_id=' + id, 'PUT')
    const articles = (await (await api('/api/items?feed_id=' + id)).json()).list
    const oldest = articles[articles.length - 1]
    await p.evaluate(id => { vm.feedSelected = 'feed:' + id; vm.filterSelected = '' }, id)
    await p.waitForFunction(() => !vm.loading.items && vm.items.length === 8)
    await p.locator(`.selectgroup[data-item-id="${oldest.id}"] .selectgroup-label`).click()
    await p.waitForFunction(id => vm.itemSelectedDetails && vm.itemSelectedDetails.id === id, oldest.id)
    await system(p)
    const input = p.getByLabel('每个订阅源已读保留上限', {exact: true})
    const save = input.locator('xpath=../..').getByRole('button', {name: '保存', exact: true})
    assert.equal(await input.inputValue(), '1000')
    let writes = 0
    p.on('request', request => { if (request.url().endsWith('/api/settings') && request.method() === 'PUT') writes++ })
    for (const value of ['', '0', '-1', '1.5', '9007199254740992']) {
      await input.fill(value)
      await save.click()
      assert.ok((await p.locator('.settings-notice').textContent()).includes('大于 0'))
      assert.equal(writes, 0)
    }
    await input.fill('2')
    await p.route('**/api/settings', route => route.request().method() === 'PUT' ? route.fulfill({status: 503}) : route.continue())
    await save.click()
    await p.waitForFunction(() => !vm.settingsPending.read_items_keep_limit)
    assert.ok((await p.locator('.settings-notice').textContent()).includes('保存失败'))
    assert.equal(await p.evaluate(() => vm.readItemsKeepLimit), 1000)
    assert.equal(await input.inputValue(), '2')
    assert.equal((await (await api('/api/items?feed_id=' + id)).json()).list.length, 8)
    await p.unroute('**/api/settings')
    await save.click()
    await p.waitForFunction(() => vm.readItemsKeepLimit === 2 && !vm.settingsPending.read_items_keep_limit && vm.itemSelected === null && vm.items.length === 2)
    assert.equal(await p.locator('.content-author').count(), 0)
    assert.equal((await (await api('/api/settings')).json()).read_items_keep_limit, 2)
    await p.reload()
    await p.waitForFunction(() => window.vm && !vm.loading.items && vm.feeds.length >= 3)
    await system(p)
    assert.equal(await input.inputValue(), '2')
    for (const width of [390, 320]) {
      await p.setViewportSize({width, height: 900})
      const sizes = await p.locator('.settings-workspace').evaluate(el => ({width: el.clientWidth, scroll: el.scrollWidth, body: document.body.scrollWidth, viewport: window.innerWidth}))
      assert.ok(sizes.scroll <= sizes.width + 1 && sizes.body <= sizes.viewport + 1, JSON.stringify(sizes))
    }
    await input.fill('4')
    await save.click()
    await p.waitForFunction(() => vm.readItemsKeepLimit === 4 && !vm.settingsPending.read_items_keep_limit)
    assert.equal((await (await api('/api/items?feed_id=' + id)).json()).list.length, 2)
  } finally {
    await api('/api/settings', 'PUT', {read_items_keep_limit: 1000})
    await api('/api/feeds/' + id, 'DELETE')
    await close(p)
  }
})

test('open source settings follow feed folder and all-source selections', async () => {
  const folder = await (await api('/api/folders', 'POST', {title: 'Selection settings'})).json()
  try {
    await api('/api/feeds/' + feeds[0].id, 'PUT', {folder_id: folder.id})
    for (const width of [1440, 390]) {
      const p = await page(width)
      await p.evaluate(() => { vm.filterSelected = '' })
      await feedSettings(p, feeds[0].id)
      await p.getByRole('button', {name: '编辑标题', exact: true}).click()
      await p.getByRole('textbox', {name: '标题', exact: true}).fill('First source draft')
      const historyLength = await p.evaluate(() => history.length)
      await p.evaluate(id => { vm.feedSelected = 'feed:' + id; return vm.$nextTick() }, feeds[1].id)
      assert.equal(await p.evaluate(() => vm.settingsFeed.id), feeds[1].id)
      assert.equal(await p.evaluate(() => vm.settings), 'feed')
      assert.equal((await p.locator('#settings-heading').textContent()).trim(), feeds[1].title)
      assert.equal(await p.evaluate(() => history.length), historyLength)
      await p.evaluate(id => { vm.feedSelected = 'folder:' + id; return vm.$nextTick() }, folder.id)
      assert.equal(await p.evaluate(() => vm.settings), 'folder')
      assert.equal(await p.evaluate(() => vm.settingsFolder.id), folder.id)
      assert.equal((await p.locator('#settings-heading').textContent()).trim(), folder.title)
      await p.evaluate(() => { vm.feedSelected = ''; return vm.$nextTick() })
      assert.equal(await p.evaluate(() => vm.settings), 'allfeeds')
      assert.equal((await p.locator('#settings-heading').textContent()).trim(), '全部订阅源设置')
      await p.evaluate(id => { vm.feedSelected = 'feed:' + id; return vm.$nextTick() }, feeds[0].id)
      assert.equal(await p.evaluate(() => vm.settingsFeed.id), feeds[0].id)
      assert.equal(await p.getByRole('textbox', {name: '标题', exact: true}).inputValue(), 'First source draft')
      if (width === 390) assert.equal(await p.evaluate(() => history.state.settingsPage.feedId), feeds[0].id)
      await p.getByRole('button', {name: '关闭设置', exact: true}).click()
      await p.waitForFunction(() => !vm.settings)
      assert.equal(await p.evaluate(() => vm.current.feed.id), feeds[0].id)
      await p.evaluate(() => vm.showSettings('auth'))
      await p.waitForFunction(() => !vm.settingsPending['auth-load'])
      await p.evaluate(id => { vm.feedSelected = 'feed:' + id; return vm.$nextTick() }, feeds[1].id)
      assert.equal(await p.evaluate(() => vm.settings), 'auth')
      await close(p)
    }
  } finally {
    await api('/api/feeds/' + feeds[0].id, 'PUT', {folder_id: null})
    await api('/api/folders/' + folder.id, 'DELETE')
  }
})

test('folder and all-source bulk buttons persist only their intended scopes', async () => {
  const folder = await (await api('/api/folders', 'POST', {title: 'Bulk target'})).json()
  const other = await (await api('/api/folders', 'POST', {title: 'Bulk empty'})).json()
  await api('/api/feeds/' + feeds[0].id, 'PUT', {folder_id: folder.id})
  const p = await page()
  const statuses = async () => (await (await api('/api/items')).json()).list.map(item => [item.id, item.status])
  const originalStatuses = await statuses()
  try {
    for (const scope of ['folder', 'allfeeds']) {
      const title = scope === 'folder' ? '文件夹设置' : '全部订阅源设置'
      await p.evaluate(({scope, id}) => {
        vm.finishClosingSettings()
        vm.feedSelected = scope === 'folder' ? 'folder:' + id : ''
      }, {scope, id: folder.id})
      await p.getByRole('button', {name: title, exact: true}).click()
      await p.locator('.settings-bulk').waitFor()
      assert.equal(await p.locator('.settings-bulk button .icon svg').count(), 6)
      const click = async name => {
        await p.getByRole('button', {name, exact: true}).click()
        await p.waitForFunction(() => !vm.settingsPending.bulk)
      }
      const checkLayout = async layout => {
        const stored = await p.evaluate(() => readArticleListLayouts())
        assert.equal(stored['folder:' + folder.id], layout)
        assert.equal(stored['feed:' + feeds[0].id], layout)
        assert.equal(stored['folder:' + other.id], scope === 'allfeeds' ? layout : undefined)
        assert.equal(stored['feed:' + feeds[1].id], scope === 'allfeeds' ? layout : undefined)
        assert.equal(stored.all, scope === 'allfeeds' ? layout : undefined)
        assert.equal(await p.evaluate(() => vm.articleListLayout), layout)
      }
      await click('全部切换卡片模式')
      await checkLayout('card')
      await p.reload()
      await p.waitForFunction(() => window.vm && vm.feeds.length >= 2 && !vm.loading.items)
      await checkLayout('card')
      await p.getByRole('button', {name: title, exact: true}).click()
      await click('全部切换列表模式')
      await checkLayout('list')

      for (const [field, label, initial] of [['unread_first', '未读优先', true], ['auto_read_scroll', '自动标为已读', false]]) {
        for (const value of [true, false]) {
          await click('全部' + (value ? '启用' : '关闭') + label)
          const [savedFolders, savedFeeds, global] = await Promise.all(['/api/folders', '/api/feeds', '/api/settings'].map(async path => (await api(path)).json()))
          for (const object of savedFolders) assert.equal(object[field], scope === 'allfeeds' || object.id === folder.id ? value : initial)
          for (const object of savedFeeds) assert.equal(object[field], scope === 'allfeeds' || object.folder_id === folder.id ? value : initial)
          assert.equal(global[field], scope === 'allfeeds' ? value : initial)
          assert.equal(await p.evaluate(field => field === 'unread_first' ? vm.itemUnreadFirst : vm.autoReadScroll, field), value)
          assert.equal(await p.locator('[role="dialog"]').count(), 0)
        }
      }
    }
    assert.deepEqual(await statuses(), originalStatuses, 'changing auto-read preferences must not mark articles read')
    await p.reload()
    await p.waitForFunction(() => window.vm && vm.feeds.length >= 2 && !vm.loading.items)
    assert.equal(await p.evaluate(() => vm.itemUnreadFirst), false)
    assert.equal(await p.evaluate(() => vm.autoReadScroll), false)
  } finally {
    await api('/api/settings/bulk', 'PUT', {field: 'unread_first', value: true})
    await api('/api/settings/bulk', 'PUT', {field: 'auto_read_scroll', value: false})
    await api('/api/feeds/' + feeds[0].id, 'PUT', {folder_id: null})
    await api('/api/folders/' + folder.id, 'DELETE')
    await api('/api/folders/' + other.id, 'DELETE')
    await close(p)
  }
})

test('bulk settings handle failures, duplicate clicks, late replies and mobile Back', async () => {
  const folder = await (await api('/api/folders', 'POST', {title: 'Bulk late reply'})).json()
  const p = await page(390)
  let release
  try {
    await p.evaluate(() => { vm.feedSelected = '' })
    const open = () => p.getByRole('button', {name: '全部订阅源设置', exact: true}).click()
    await open()
    await p.locator('.settings-bulk').waitFor()
    await p.goBack()
    await p.waitForFunction(() => !vm.settings)
    assert.equal(await p.evaluate(() => vm.feedSelected), '')
    await open()
    await p.route('**/api/settings/bulk', route => route.fulfill({status: 500}))
    await p.getByRole('button', {name: '全部关闭未读优先', exact: true}).click()
    await p.waitForFunction(() => !vm.settingsPending.bulk && vm.settingsNotice.includes('保存失败'))
    assert.equal(await p.evaluate(() => vm.itemUnreadFirst), true)
    assert.equal((await (await api('/api/settings')).json()).unread_first, true)
    await p.unroute('**/api/settings/bulk')

    const oldLayout = await p.evaluate(() => vm.articleListLayout)
    const oldStorage = await p.evaluate(() => localStorage.getItem(ARTICLE_LIST_LAYOUTS_KEY))
    await p.evaluate(() => {
      window.bulkOriginalSetItem = Storage.prototype.setItem
      Storage.prototype.setItem = function(key, value) {
        if (key === ARTICLE_LIST_LAYOUTS_KEY) throw new Error('Storage blocked')
        return window.bulkOriginalSetItem.call(this, key, value)
      }
    })
    await p.getByRole('button', {name: '全部切换卡片模式', exact: true}).click()
    await p.waitForFunction(() => !vm.settingsPending.bulk && vm.settingsNotice.includes('存储权限'))
    assert.equal(await p.evaluate(() => vm.articleListLayout), oldLayout)
    assert.equal(await p.evaluate(() => localStorage.getItem(ARTICLE_LIST_LAYOUTS_KEY)), oldStorage)
    await p.evaluate(() => { Storage.prototype.setItem = window.bulkOriginalSetItem })

    let started, requests = 0
    const requested = new Promise(resolve => { started = resolve })
    const delayed = new Promise(resolve => { release = resolve })
    await p.route('**/api/settings/bulk', async route => {
      requests++
      started()
      await delayed
      await route.continue()
    })
    await p.getByRole('button', {name: '全部关闭未读优先', exact: true}).click()
    await requested
    assert.equal(await p.locator('.settings-bulk button:disabled').count(), 6)
    assert.equal(await p.evaluate(() => vm.saveBulkSetting('unread_first', true)), false)
    assert.equal(requests, 1)
    await p.evaluate(id => { vm.feedSelected = 'folder:' + id }, folder.id)
    await p.waitForFunction(id => vm.settingsFolder && vm.settingsFolder.id === id, folder.id)
    release()
    await p.waitForFunction(() => !vm.settingsPending.bulk)
    assert.equal(await p.evaluate(() => vm.settings), 'folder')
    assert.equal(await p.evaluate(() => vm.itemUnreadFirst), false)
    assert.equal(await p.evaluate(() => vm.settingsNotice), '')
    assert.equal(await p.evaluate(() => vm.settingsNotices['allfeeds:']), '已保存。')
  } finally {
    if (release) release()
    await api('/api/settings/bulk', 'PUT', {field: 'unread_first', value: true})
    await api('/api/folders/' + folder.id, 'DELETE')
    await close(p)
  }
})
