(() => {
  const fileInput = document.getElementById('file')
  const pick = document.getElementById('pick')
  const drop = document.getElementById('drop')
  const paste = document.getElementById('paste')
  const meta = document.getElementById('meta')
  const errorEl = document.getElementById('error')
  const preview = document.getElementById('preview')
  const previewCount = document.getElementById('previewCount')
  const downloadBtn = document.getElementById('download')
  const copyBtn = document.getElementById('copy')
  const output = document.getElementById('output')
  const outputPanel = document.getElementById('outputPanel')
  const outputHint = document.getElementById('outputHint')

  let sourceName = 'accounts.json'
  let source = null

  const fields = [...document.querySelectorAll('[data-key]')].map((box) => ({
    box,
    input: document.querySelector(`[data-input="${box.dataset.key}"]`),
  }))

  fields.forEach(({ box, input }) => {
    if (input) input.disabled = !box.checked
    box.addEventListener('change', () => {
      if (input) input.disabled = !box.checked
      refresh()
    })
    input?.addEventListener('input', refresh)
    input?.addEventListener('change', refresh)
  })

  pick.addEventListener('click', () => fileInput.click())
  fileInput.addEventListener('change', () => {
    const file = fileInput.files && fileInput.files[0]
    if (file) readFile(file)
  })
  paste.addEventListener('input', () => {
    if (paste.value.trim()) loadText(paste.value, 'pasted.json')
  })

  ;['dragenter', 'dragover'].forEach((type) => {
    drop.addEventListener(type, (event) => {
      event.preventDefault()
      drop.classList.add('over')
    })
  })
  ;['dragleave', 'drop'].forEach((type) => {
    drop.addEventListener(type, (event) => {
      event.preventDefault()
      drop.classList.remove('over')
    })
  })
  drop.addEventListener('drop', (event) => {
    const file = event.dataTransfer && event.dataTransfer.files[0]
    if (file) readFile(file)
  })

  downloadBtn.addEventListener('click', () => {
    const result = showResult()
    if (!result) return
    const blob = new Blob([result.text], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = result.name
    link.rel = 'noopener'
    link.style.display = 'none'
    document.body.appendChild(link)
    link.click()
    window.setTimeout(() => {
      link.remove()
      URL.revokeObjectURL(url)
    }, 1000)
    outputHint.textContent = '若浏览器没有弹出下载，请在下方全选复制后另存为 .json'
  })

  copyBtn.addEventListener('click', async () => {
    const result = showResult()
    if (!result) return
    try {
      await navigator.clipboard.writeText(result.text)
      copyBtn.textContent = '已复制'
      outputHint.textContent = '已复制到剪贴板'
      setTimeout(() => {
        copyBtn.textContent = '复制到剪贴板'
      }, 1600)
    } catch {
      output.focus()
      output.select()
      outputHint.textContent = '剪贴板不可用，已选中下方文本，按 Ctrl+C 复制'
    }
  })

  function readFile(file) {
    const reader = new FileReader()
    reader.onload = () => loadText(String(reader.result || ''), file.name)
    reader.readAsText(file)
  }

  function loadText(text, name) {
    hideError()
    try {
      source = parsePacket(text)
      sourceName = name || 'accounts.json'
      paste.value = ''
      refresh()
    } catch (error) {
      source = null
      showError(error instanceof Error ? error.message : '无法解析 JSON')
      refresh()
    }
  }

  function parsePacket(text) {
    const raw = JSON.parse(text)
    if (Array.isArray(raw)) {
      return { accounts: raw, proxies: [] }
    }
    if (raw && Array.isArray(raw.data?.accounts)) {
      return raw.data
    }
    if (raw && Array.isArray(raw.accounts)) {
      return raw
    }
    throw new Error('不是 Sub2API 账号数据包：需要顶层 accounts 数组')
  }

  function selected() {
    const values = {}
    for (const { box, input } of fields) {
      if (!box.checked) continue
      values[box.dataset.key] = input ? input.value : true
    }
    return values
  }

  function applyOverrides(packet, values) {
    const next = structuredClone(packet)
    const accounts = Array.isArray(next.accounts) ? next.accounts : []
    let extraMerge = null
    if ('extraMerge' in values) {
      const raw = String(values.extraMerge || '').trim()
      extraMerge = raw ? JSON.parse(raw) : {}
      if (!extraMerge || typeof extraMerge !== 'object' || Array.isArray(extraMerge)) {
        throw new Error('合并 extra 必须是 JSON 对象')
      }
    }
    const extraRemove = String(values.extraRemove || '')
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)

    for (const account of accounts) {
      if ('namePrefix' in values) account.name = `${values.namePrefix || ''}${account.name || ''}`
      if ('nameSuffix' in values) account.name = `${account.name || ''}${values.nameSuffix || ''}`
      if ('notes' in values) {
        const notes = String(values.notes || '')
        if (notes) account.notes = notes
        else delete account.notes
      }
      if ('concurrency' in values) account.concurrency = Number(values.concurrency)
      if ('priority' in values) account.priority = Number(values.priority)
      if ('rateMultiplier' in values) account.rate_multiplier = Number(values.rateMultiplier)
      if ('autoPause' in values) account.auto_pause_on_expired = values.autoPause === 'true'
      if (extraMerge) {
        account.extra = { ...(account.extra && typeof account.extra === 'object' ? account.extra : {}), ...extraMerge }
      }
      if ('extraRemove' in values && account.extra && typeof account.extra === 'object') {
        for (const key of extraRemove) delete account.extra[key]
      }
      if (values.clearProxy) delete account.proxy_key
      if (values.clearManaged) delete account.managed_proxy
    }

    if (values.clearProxy && Array.isArray(next.proxies)) {
      const used = new Set(accounts.map((item) => item.proxy_key).filter(Boolean))
      next.proxies = next.proxies.filter((item) => used.has(item.proxy_key))
    }
    if (values.clearManaged && Array.isArray(next.managed_proxy_providers)) {
      const used = new Set(accounts.map((item) => item.managed_proxy?.provider_key).filter(Boolean))
      next.managed_proxy_providers = next.managed_proxy_providers.filter((item) => used.has(item.provider_key))
    }
    return next
  }

  function transform() {
    if (!source) return null
    try {
      hideError()
      const packet = applyOverrides(source, selected())
      const stem = sourceName.replace(/\.json$/i, '')
      return {
        name: `${stem}-overridden.json`,
        text: `${JSON.stringify(packet, null, 2)}\n`,
        packet,
      }
    } catch (error) {
      showError(error instanceof Error ? error.message : '覆盖失败')
      return null
    }
  }

  function showResult() {
    const result = transform()
    if (!result) {
      outputPanel.hidden = true
      output.value = ''
      return null
    }
    outputPanel.hidden = false
    output.value = result.text
    return result
  }

  function refresh() {
    const result = source ? transform() : null
    const packet = result?.packet
    const accounts = packet?.accounts || []
    renderMeta(packet)
    preview.innerHTML = ''
    previewCount.textContent = source ? `${accounts.length} 个账号` : ''
    downloadBtn.disabled = !result
    copyBtn.disabled = !result
    if (!outputPanel.hidden) {
      output.value = result ? result.text : ''
      if (!result) outputPanel.hidden = true
    }
    for (const account of accounts.slice(0, 12)) {
      const row = document.createElement('tr')
      row.innerHTML = `
        <td>${escapeHtml(account.name || '')}</td>
        <td>${escapeHtml(account.platform || '')}</td>
        <td>${escapeHtml(String(account.concurrency ?? ''))}</td>
        <td>${escapeHtml(String(account.priority ?? ''))}</td>
        <td>${escapeHtml(proxyLabel(account))}</td>
      `
      preview.appendChild(row)
    }
  }

  function renderMeta(packet) {
    if (!packet) {
      meta.hidden = true
      meta.innerHTML = ''
      return
    }
    const accounts = packet.accounts || []
    const chips = [
      `${accounts.length} 个账号`,
      packet.type ? `type ${packet.type}` : null,
      packet.version ? `v${packet.version}` : null,
      packet.format ? `format ${packet.format}` : null,
      `${(packet.proxies || []).length} 个静态代理`,
      `${accounts.filter((item) => item.managed_proxy).length} 个受管绑定`,
    ].filter(Boolean)
    meta.hidden = false
    meta.innerHTML = chips.map((item) => `<span class="chip">${escapeHtml(item)}</span>`).join('')
  }

  function proxyLabel(account) {
    if (account.managed_proxy?.provider_key) return `受管 ${account.managed_proxy.provider_key}`
    if (account.proxy_key) return '静态代理'
    return '无'
  }

  function showError(message) {
    errorEl.hidden = false
    errorEl.textContent = message
  }

  function hideError() {
    errorEl.hidden = true
    errorEl.textContent = ''
  }

  function escapeHtml(value) {
    return String(value)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
  }
})()
