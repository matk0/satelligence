import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = [
    "messages", "input", "sendBtn", "connectBtn",
    "statusDot", "statusText", "modal", "nwcInput",
    "modelSelect", "costInfo"
  ]

  connect() {
    this.nwcConnection = null
    this.messages = []
    this.apiBase = this.getApiBase()

    // Check if Alby/WebLN is available
    if (window.webln) {
      this.statusTextTarget.textContent = "Alby detected"
    }
  }

  getApiBase() {
    // Use environment-appropriate API URL
    if (window.location.hostname === 'localhost') {
      return 'http://localhost:8080'
    }
    return 'https://api.satilligence.com'
  }

  connectWallet() {
    this.modalTarget.classList.remove('hidden')
  }

  closeModal() {
    this.modalTarget.classList.add('hidden')
  }

  async connectWebLN() {
    if (!window.webln) {
      alert('Alby extension not detected. Please install it from getalby.com')
      return
    }

    try {
      await window.webln.enable()

      // Get NWC connection from Alby
      // Note: This requires Alby to support NWC export
      // For now, we'll use WebLN directly
      this.useWebLN = true
      this.setConnected('Alby (WebLN)')
      this.closeModal()
    } catch (error) {
      console.error('WebLN connection failed:', error)
      alert('Failed to connect: ' + error.message)
    }
  }

  connectNWC() {
    const nwcUrl = this.nwcInputTarget.value.trim()

    if (!nwcUrl.startsWith('nostr+walletconnect://')) {
      alert('Invalid NWC URL. It should start with nostr+walletconnect://')
      return
    }

    this.nwcConnection = nwcUrl
    this.useWebLN = false
    this.setConnected('NWC')
    this.closeModal()
  }

  setConnected(method) {
    this.statusDotTarget.classList.remove('bg-red-500')
    this.statusDotTarget.classList.add('bg-green-500')
    this.statusTextTarget.textContent = `Connected (${method})`
    this.connectBtnTarget.textContent = 'Connected'
    this.connectBtnTarget.classList.remove('bg-yellow-500', 'hover:bg-yellow-400')
    this.connectBtnTarget.classList.add('bg-green-600')

    // Enable input
    this.inputTarget.disabled = false
    this.sendBtnTarget.disabled = false
    this.inputTarget.focus()
  }

  handleKeydown(event) {
    if (event.ctrlKey && event.key === 'Enter') {
      this.sendMessage()
    }
  }

  async sendMessage() {
    const content = this.inputTarget.value.trim()
    if (!content) return

    if (!this.nwcConnection && !this.useWebLN) {
      alert('Please connect your wallet first')
      return
    }

    // Add user message to UI
    this.addMessage('user', content)
    this.inputTarget.value = ''

    // Disable input while processing
    this.inputTarget.disabled = true
    this.sendBtnTarget.disabled = true
    this.sendBtnTarget.textContent = 'Sending...'

    // Add assistant placeholder
    const assistantDiv = this.addMessage('assistant', '...')

    try {
      // Build messages array
      this.messages.push({ role: 'user', content })

      const response = await this.callAPI()

      // Update assistant message
      const assistantContent = response.choices[0].message.content
      assistantDiv.querySelector('.message-content').textContent = assistantContent
      this.messages.push({ role: 'assistant', content: assistantContent })

      // Show cost info
      this.showCostInfo()
    } catch (error) {
      console.error('API error:', error)
      assistantDiv.querySelector('.message-content').textContent = 'Error: ' + error.message
      assistantDiv.classList.add('border-red-500')
    } finally {
      this.inputTarget.disabled = false
      this.sendBtnTarget.disabled = false
      this.sendBtnTarget.textContent = 'Send'
      this.inputTarget.focus()
    }
  }

  async callAPI() {
    const model = this.modelSelectTarget.value

    if (this.useWebLN) {
      // For WebLN, we need a different flow
      // The user pays via WebLN when prompted
      return this.callAPIWithWebLN(model)
    }

    // NWC flow - server handles payment
    const response = await fetch(`${this.apiBase}/v1/nwc/chat/completions`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-NWC': this.nwcConnection
      },
      body: JSON.stringify({
        model,
        messages: this.messages,
        max_tokens: 1000
      })
    })

    if (!response.ok) {
      const error = await response.json()
      throw new Error(error.error?.message || 'Request failed')
    }

    // Capture cost headers
    this.lastChargedSats = response.headers.get('X-Charged-Sats')
    this.lastCostSats = response.headers.get('X-Cost-Sats')
    this.lastRefundSats = response.headers.get('X-Refund-Sats')
    this.lastRefundStatus = response.headers.get('X-Refund-Status')

    return response.json()
  }

  async callAPIWithWebLN() {
    // WebLN flow requires two-step process:
    // 1. Get invoice from server
    // 2. Pay via WebLN
    // 3. Submit paid invoice

    // For now, show a message that WebLN direct integration is coming
    // Users should use NWC connection string instead
    throw new Error('WebLN direct payment coming soon. Please use NWC connection string instead.')
  }

  addMessage(role, content) {
    // Remove welcome message if present
    const welcome = this.messagesTarget.querySelector('.text-center')
    if (welcome) welcome.remove()

    const div = document.createElement('div')
    div.className = role === 'user'
      ? 'flex justify-end'
      : 'flex justify-start'

    const bubble = document.createElement('div')
    bubble.className = role === 'user'
      ? 'max-w-[80%] bg-yellow-500 text-black rounded-2xl rounded-br-md px-4 py-3'
      : 'max-w-[80%] bg-slate-700 text-white rounded-2xl rounded-bl-md px-4 py-3 border border-slate-600'

    const contentSpan = document.createElement('span')
    contentSpan.className = 'message-content whitespace-pre-wrap'
    contentSpan.textContent = content

    bubble.appendChild(contentSpan)
    div.appendChild(bubble)
    this.messagesTarget.appendChild(div)

    // Scroll to bottom
    this.messagesTarget.scrollTop = this.messagesTarget.scrollHeight

    return div
  }

  showCostInfo() {
    if (this.lastCostSats) {
      let info = `Cost: ${this.lastCostSats} sats`
      if (this.lastRefundSats && this.lastRefundSats !== '0') {
        info += ` (refunded ${this.lastRefundSats})`
      }
      this.costInfoTarget.textContent = info
      this.costInfoTarget.classList.remove('text-gray-500')
      this.costInfoTarget.classList.add('text-green-400')

      // Reset after 5 seconds
      setTimeout(() => {
        this.costInfoTarget.textContent = ''
      }, 5000)
    }
  }
}
