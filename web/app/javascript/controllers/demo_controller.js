import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = [
    "nwcInput",
    "messageInput",
    "modelSelect",
    "sendBtn",
    "albyBtn",
    "timeline", "timelineItems",
    "response", "responseContent",
    "costDisplay", "costUsdDisplay", "refundDisplay",
    "chatContinuation", "continuationInput", "continuationBtn"
  ]

  // Step definitions with professional labels
  static steps = {
    wallet_connect: { label: "Connecting to wallet", icon: "wallet" },
    moderation: { label: "Content moderation", icon: "shield" },
    cost_estimate: { label: "Estimating cost", icon: "calculator" },
    invoice_create: { label: "Creating invoice", icon: "receipt" },
    payment_request: { label: "Processing payment", icon: "lightning" },
    generation: { label: "Generating response", icon: "brain" },
    cost_calculate: { label: "Calculating final cost", icon: "calculator" },
    refund: { label: "Processing refund", icon: "refund" }
  }

  connect() {
    this.loadModels()
    this.checkAlbyAvailable()
    this.messages = []
    this.totalCostSats = 0
    this.totalCostUsd = 0
    this.stepElements = {}
  }

  checkAlbyAvailable() {
    if (!window.webln && this.hasAlbyBtnTarget) {
      this.albyBtnTarget.style.display = 'none'
    }
  }

  async connectAlby() {
    if (!window.webln) {
      window.open('https://getalby.com', '_blank')
      return
    }

    try {
      await window.webln.enable()
      alert('Alby connected! To use this demo, copy your NWC connection string from:\n\nAlby Extension -> Settings -> Developer -> Nostr Wallet Connect\n\nThen paste it in the X-NWC field.')
    } catch (error) {
      console.error('Alby connection error:', error)
      alert('Failed to connect to Alby: ' + error.message)
    }
  }

  async loadModels() {
    try {
      const response = await fetch("/api/models")
      if (!response.ok) throw new Error("Failed to fetch models")
      const data = await response.json()
      const models = data.models || []

      this.modelSelectTarget.innerHTML = ""
      models.forEach((model, index) => {
        const option = document.createElement("option")
        option.value = model
        option.textContent = model
        if (index === 0) option.selected = true
        this.modelSelectTarget.appendChild(option)
      })

      if (models.length === 0) {
        const option = document.createElement("option")
        option.value = "gpt-5.2"
        option.textContent = "gpt-5.2"
        this.modelSelectTarget.appendChild(option)
      }
    } catch (error) {
      console.error("Failed to load models:", error)
      this.modelSelectTarget.innerHTML = '<option value="gpt-5.2">gpt-5.2</option>'
    }
  }

  async send() {
    const nwc = this.nwcInputTarget.value.trim()
    const message = this.messageInputTarget.value.trim()
    const model = this.modelSelectTarget.value

    if (!nwc) {
      alert("Please enter your NWC connection string")
      return
    }

    if (!nwc.startsWith("nostr+walletconnect://")) {
      alert("Invalid NWC URL. It should start with nostr+walletconnect://")
      return
    }

    if (!message) {
      alert("Please enter a message")
      return
    }

    this.messages.push({ role: "user", content: message })
    await this.sendMessage(nwc, model, true)
  }

  async sendContinuation() {
    const nwc = this.nwcInputTarget.value.trim()
    const message = this.continuationInputTarget.value.trim()
    const model = this.modelSelectTarget.value

    if (!message) return

    this.messages.push({ role: "user", content: message })
    this.continuationInputTarget.value = ""
    await this.sendMessage(nwc, model, false)
  }

  async sendMessage(nwc, model, isFirstMessage) {
    // Disable buttons
    if (isFirstMessage) {
      this.sendBtnTarget.disabled = true
      this.sendBtnTarget.textContent = "Processing..."
    } else {
      this.continuationBtnTarget.disabled = true
      this.continuationBtnTarget.textContent = "..."
    }

    // Reset and show timeline
    this.timelineTarget.classList.remove("hidden")
    this.timelineItemsTarget.innerHTML = ""
    this.stepElements = {}

    // Show response area
    this.responseTarget.classList.remove("hidden")

    // Add user message to response
    const userMsg = this.messages[this.messages.length - 1].content
    if (isFirstMessage) {
      this.responseContentTarget.innerHTML = `<div class="text-cyan-400 font-medium mb-2">You:</div><div class="mb-4 whitespace-pre-wrap">${this.escapeHtml(userMsg)}</div>`
    } else {
      this.responseContentTarget.innerHTML += `<div class="text-cyan-400 font-medium mb-2 mt-4">You:</div><div class="mb-4 whitespace-pre-wrap">${this.escapeHtml(userMsg)}</div>`
    }

    // Add assistant response placeholder
    this.responseContentTarget.innerHTML += `<div class="text-green-400 font-medium mb-2">Assistant:</div><div id="streaming-content" class="whitespace-pre-wrap"></div>`
    const streamingContent = document.getElementById('streaming-content')

    try {
      const response = await fetch("/api/nwc/chat/stream", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-NWC": nwc
        },
        body: JSON.stringify({
          model: model,
          messages: this.messages,
          max_tokens: 1000
        })
      })

      if (!response.ok) {
        const error = await response.text()
        throw new Error(error || "Request failed")
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ""
      let fullContent = ""
      let finalData = null

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split("\n")
        buffer = lines.pop() || ""

        for (const line of lines) {
          if (line.startsWith("event: ")) {
            // Event type line - data follows on next line
            continue
          }

          if (line.startsWith("data: ")) {
            const data = line.substring(6)
            try {
              const parsed = JSON.parse(data)
              this.handleStreamEvent(parsed, streamingContent)

              // Accumulate content from tokens
              if (parsed.content) {
                fullContent += parsed.content
              }

              // Capture final summary
              if (parsed.charged_sats !== undefined) {
                finalData = parsed
              }
            } catch (e) {
              // Ignore parse errors for partial data
            }
          }
        }
      }

      // Process final data
      if (finalData) {
        this.totalCostSats += finalData.cost_sats || 0
        this.totalCostUsd += finalData.cost_usd || 0

        this.costDisplayTarget.textContent = this.totalCostSats
        this.costUsdDisplayTarget.textContent = `$${this.totalCostUsd.toFixed(6)}`
        this.refundDisplayTarget.textContent = finalData.refund_sats || 0

        // Store assistant response
        this.messages.push({ role: "assistant", content: finalData.content || fullContent })
      } else if (fullContent) {
        this.messages.push({ role: "assistant", content: fullContent })
      }

      // Show chat continuation UI
      this.chatContinuationTarget.classList.remove("hidden")
      this.continuationInputTarget.focus()

    } catch (error) {
      console.error("Demo error:", error)
      this.addTimelineItem("error", `Error: ${error.message}`, "error")

      // Remove the failed user message from history
      this.messages.pop()

      // Show error in response
      streamingContent.innerHTML = `<span class="text-red-400">Error: ${this.escapeHtml(error.message)}</span>`
    } finally {
      if (isFirstMessage) {
        this.sendBtnTarget.disabled = false
        this.sendBtnTarget.textContent = "Send Request"
      } else {
        this.continuationBtnTarget.disabled = false
        this.continuationBtnTarget.textContent = "Send"
      }
    }
  }

  handleStreamEvent(data, streamingContent) {
    // Handle step updates
    if (data.step) {
      const stepInfo = this.constructor.steps[data.step]
      if (stepInfo) {
        if (data.status === "pending") {
          this.addStep(data.step, stepInfo.label, stepInfo.icon, "pending", data)
        } else if (data.status === "complete") {
          this.updateStep(data.step, "complete", data)
        } else if (data.status === "error") {
          this.updateStep(data.step, "error", data)
        }
      }
      return
    }

    // Handle token streaming
    if (data.content) {
      streamingContent.textContent += data.content
      // Auto-scroll to bottom
      streamingContent.scrollIntoView({ behavior: "smooth", block: "end" })
    }

    // Handle errors
    if (data.code && data.message) {
      this.addTimelineItem("error", data.message, "error")
    }
  }

  addStep(stepId, label, icon, status, data) {
    const icons = {
      wallet: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 10h18M7 15h1m4 0h1m-7 4h12a3 3 0 003-3V8a3 3 0 00-3-3H6a3 3 0 00-3 3v8a3 3 0 003 3z"></path></svg>',
      shield: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z"></path></svg>',
      calculator: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 7h6m0 10v-3m-3 3h.01M9 17h.01M9 14h.01M12 14h.01M15 11h.01M12 11h.01M9 11h.01M7 21h10a2 2 0 002-2V5a2 2 0 00-2-2H7a2 2 0 00-2 2v14a2 2 0 002 2z"></path></svg>',
      receipt: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2"></path></svg>',
      lightning: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"></path></svg>',
      brain: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3m3.343-5.657l-.707-.707m2.828 9.9a5 5 0 117.072 0l-.548.547A3.374 3.374 0 0014 18.469V19a2 2 0 11-4 0v-.531c0-.895-.356-1.754-.988-2.386l-.548-.547z"></path></svg>',
      refund: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 10h10a8 8 0 018 8v2M3 10l6 6m-6-6l6-6"></path></svg>',
      check: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path></svg>',
      error: '<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path></svg>'
    }

    const statusColors = {
      pending: "text-yellow-400",
      complete: "text-green-400",
      error: "text-red-400"
    }

    // Build detail string
    let details = ""
    if (data.estimated_sats) details = `(est. ${data.estimated_sats} sats)`
    if (data.charge_sats) details = `(${data.charge_sats} sats)`
    if (data.amount_sats) details = `(${data.amount_sats} sats)`

    const div = document.createElement("div")
    div.id = `step-${stepId}`
    div.className = `flex items-center gap-3 py-1 ${statusColors[status]}`
    div.innerHTML = `
      <span class="flex-shrink-0">${icons[icon] || icons.check}</span>
      <span class="flex-grow">${label}</span>
      <span class="text-sm opacity-75">${details}</span>
      ${status === "pending" ? '<span class="flex-shrink-0"><svg class="animate-spin h-4 w-4" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg></span>' : ""}
    `

    this.timelineItemsTarget.appendChild(div)
    this.stepElements[stepId] = div
  }

  updateStep(stepId, status, data) {
    const div = this.stepElements[stepId]
    if (!div) return

    const statusColors = {
      pending: "text-yellow-400",
      complete: "text-green-400",
      error: "text-red-400"
    }

    // Update class
    div.className = `flex items-center gap-3 py-1 ${statusColors[status]}`

    // Build detail string
    let details = ""
    if (data.cost_sats !== undefined) details = `(${data.cost_sats} sats / $${(data.cost_usd || 0).toFixed(6)})`
    if (data.refund_sats !== undefined && stepId === "refund") details = `(${data.refund_sats} sats)`
    if (data.amount_sats !== undefined && stepId !== "cost_calculate") details = `(${data.amount_sats} sats)`

    // Update content - keep label, update status indicator and details
    const spans = div.querySelectorAll("span")
    if (spans.length >= 3) {
      spans[2].textContent = details
    }

    // Remove spinner, add check or error
    const spinner = div.querySelector(".animate-spin")
    if (spinner) {
      const parent = spinner.parentElement
      if (status === "complete") {
        parent.innerHTML = '<svg class="w-4 h-4 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path></svg>'
      } else if (status === "error") {
        parent.innerHTML = '<svg class="w-4 h-4 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path></svg>'
      }
    }
  }

  addTimelineItem(_icon, text, status) {
    const statusColors = {
      pending: "text-yellow-400",
      complete: "text-green-400",
      error: "text-red-400"
    }

    const div = document.createElement("div")
    div.className = `flex items-center gap-3 py-1 ${statusColors[status]}`
    div.innerHTML = `
      <span class="flex-shrink-0"><svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"></path></svg></span>
      <span>${text}</span>
    `
    this.timelineItemsTarget.appendChild(div)
  }

  escapeHtml(text) {
    const div = document.createElement('div')
    div.textContent = text
    return div.innerHTML
  }
}
