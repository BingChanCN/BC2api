(() => {
  const board = document.getElementById('board')
  const scoreEl = document.getElementById('score')
  const bestEl = document.getElementById('best')
  const statusEl = document.getElementById('status')
  const startBtn = document.getElementById('start')
  const resetBtn = document.getElementById('reset')

  const size = 9
  const cells = []
  let score = 0
  let best = null
  let activeIndex = -1
  let armedAt = 0
  let timer = 0
  let running = false

  for (let i = 0; i < size; i += 1) {
    const cell = document.createElement('button')
    cell.type = 'button'
    cell.className = 'cell'
    cell.dataset.index = String(i)
    cell.setAttribute('aria-label', `格子 ${i + 1}`)
    cell.addEventListener('click', () => onCellClick(i))
    board.appendChild(cell)
    cells.push(cell)
  }

  function setStatus(text) {
    statusEl.textContent = text
  }

  function clearActive() {
    if (activeIndex >= 0) {
      cells[activeIndex].classList.remove('active')
      activeIndex = -1
    }
  }

  function scheduleNext() {
    clearActive()
    if (!running) return
    setStatus('等待…')
    const delay = 500 + Math.floor(Math.random() * 1400)
    timer = window.setTimeout(() => {
      activeIndex = Math.floor(Math.random() * size)
      cells[activeIndex].classList.add('active')
      armedAt = performance.now()
      setStatus('快点击！')
    }, delay)
  }

  function onCellClick(index) {
    if (!running || index !== activeIndex) return
    const reaction = Math.round(performance.now() - armedAt)
    score += 1
    scoreEl.textContent = String(score)
    if (best === null || reaction < best) {
      best = reaction
      bestEl.textContent = `${best} ms`
    }
    setStatus(`${reaction} ms`)
    scheduleNext()
  }

  function stop() {
    running = false
    window.clearTimeout(timer)
    clearActive()
    startBtn.disabled = false
    setStatus('已停止')
  }

  startBtn.addEventListener('click', () => {
    if (running) return
    running = true
    startBtn.disabled = true
    scheduleNext()
  })

  resetBtn.addEventListener('click', () => {
    stop()
    score = 0
    best = null
    scoreEl.textContent = '0'
    bestEl.textContent = '—'
    setStatus('准备')
  })
})()
