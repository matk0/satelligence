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

    // Check if Alby/WebLN is available
    if (window.webln) {
      this.statusTextTarget.textContent = "Alby detected"
    }
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

    // Prevent double submissions
    if (this.isProcessing) return
    this.isProcessing = true

    if (!this.nwcConnection && !this.useWebLN) {
      alert('Please connect your wallet first')
      this.isProcessing = false
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
      this.isProcessing = false
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
    const response = await fetch('/api/nwc/chat', {
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
    const model = this.modelSelectTarget.value

    // Step 1: Get a quote (invoice) from the server
    this.sendBtnTarget.textContent = 'Getting quote...'

    const quoteResponse = await fetch('/api/webln/quote', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        model,
        messages: this.messages,
        max_tokens: 1000
      })
    })

    if (!quoteResponse.ok) {
      const error = await quoteResponse.json()
      throw new Error(error.error?.message || 'Failed to get quote')
    }

    const quote = await quoteResponse.json()

    // Step 2: Pay the invoice via WebLN
    this.sendBtnTarget.textContent = `Paying ${quote.amount_sats} sats...`

    try {
      await window.webln.sendPayment(quote.payment_request)
    } catch (error) {
      throw new Error('Payment cancelled or failed: ' + error.message)
    }

    // Step 3: Submit the payment hash to get the response
    this.sendBtnTarget.textContent = 'Processing...'

    const response = await fetch('/api/webln/chat', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Payment-Hash': quote.payment_hash
      }
    })

    if (!response.ok) {
      const error = await response.json()
      throw new Error(error.error?.message || 'Request failed')
    }

    // Capture cost headers
    this.lastChargedSats = response.headers.get('X-Charged-Sats')
    this.lastCostSats = response.headers.get('X-Cost-Sats')
    this.lastRefundSats = response.headers.get('X-Refund-Sats')

    const result = await response.json()

    // Step 4: Process refund in background (don't block the response)
    const refundSats = parseInt(this.lastRefundSats || '0', 10)
    if (refundSats > 0) {
      // Use setTimeout to ensure this runs after the current call stack completes
      setTimeout(() => this.processRefund(refundSats), 100)
    }

    return result
  }

  async processRefund(amountSats) {
    try {
      this.sendBtnTarget.textContent = `Refunding ${amountSats} sats...`

      // Request invoice from user's wallet
      const invoice = await window.webln.makeInvoice({
        amount: amountSats,
        defaultMemo: 'Satilligence refund'
      })

      // Submit invoice to backend for payment
      const refundResponse = await fetch('/api/webln/refund', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          payment_request: invoice.paymentRequest
        })
      })

      if (refundResponse.ok) {
        console.log('Refund processed successfully')
      } else {
        console.error('Refund failed')
      }
    } catch (error) {
      console.error('Refund error:', error)
      // Don't throw - refund failure shouldn't break the main flow
    }
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
