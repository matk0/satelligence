import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = [
    "nwcInput",
    "messageInput",
    "modelSelect",
    "sendBtn",
    "chatContainer",
    "chatContinuation", "continuationInput", "continuationBtn"
  ]

  // Step definitions with professional labels
  static steps = {
    wallet_connect: { label: "Connecting to wallet", icon: "wallet" },
    moderation: { label: "Content moderation", icon: "shield" },
    balance_check: { label: "Checking balance", icon: "calculator" },
    generation: { label: "Generating response", icon: "brain" },
    payment: { label: "Processing payment", icon: "lightning" }
  }

  connect() {
    this.loadModels()
    this.messages = []
    this.currentStepElements = {}
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
    await this.sendMessage(nwc, model)
  }

  async sendContinuation() {
    const nwc = this.nwcInputTarget.value.trim()
    const message = this.continuationInputTarget.value.trim()
    const model = this.modelSelectTarget.value

    if (!message) return

    this.messages.push({ role: "user", content: message })
    this.continuationInputTarget.value = ""
    await this.sendMessage(nwc, model)
  }

  async sendMessage(nwc, model) {
    // Disable buttons
    this.sendBtnTarget.disabled = true
    this.sendBtnTarget.textContent = "Processing..."
    if (this.hasContinuationBtnTarget) {
      this.continuationBtnTarget.disabled = true
      this.continuationBtnTarget.textContent = "..."
    }

    // Show chat container
    this.chatContainerTarget.classList.remove("hidden")

    // Add user message bubble
    const userMsg = this.messages[this.messages.length - 1].content
    this.addUserMessage(userMsg)

    // Add processing steps block
    const stepsBlock = this.createStepsBlock()
    this.chatContainerTarget.appendChild(stepsBlock)
    this.currentStepElements = {}
    this.currentStepsContainer = stepsBlock.querySelector('.steps-container')

    // Add assistant response block (will be filled during streaming)
    const responseBlock = this.createResponseBlock()
    this.chatContainerTarget.appendChild(responseBlock)

    const streamingContent = responseBlock.querySelector('.streaming-content')
    const costDisplay = responseBlock.querySelector('.cost-display')
    const costUsdDisplay = responseBlock.querySelector('.cost-usd-display')
    const chargeStatusDisplay = responseBlock.querySelector('.charge-status-display')

    // Scroll to show the new content
    stepsBlock.scrollIntoView({ behavior: "smooth", block: "start" })

    try {
      const response = await fetch("/api/chat/stream", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Authorization": `Bearer ${nwc}`
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
            continue
          }

          if (line.startsWith("data: ")) {
            const data = line.substring(6)
            try {
              const parsed = JSON.parse(data)
              this.handleStreamEvent(parsed, streamingContent)

              if (parsed.content) {
                fullContent += parsed.content
              }

              if (parsed.charged_sats !== undefined) {
                finalData = parsed
              }
            } catch (e) {
              // Ignore parse errors
            }
          }
        }
      }

      // Update cost display
      if (finalData) {
        costDisplay.textContent = finalData.cost_sats || 0
        costUsdDisplay.textContent = `$${(finalData.cost_usd || 0).toFixed(6)}`
        chargeStatusDisplay.textContent = finalData.charge_status || 'unknown'

        this.messages.push({ role: "assistant", content: finalData.content || fullContent })
      } else if (fullContent) {
        this.messages.push({ role: "assistant", content: fullContent })
      }

      // Mark steps block as complete
      stepsBlock.classList.add('opacity-50')

      // Show chat continuation UI
      this.chatContinuationTarget.classList.remove("hidden")
      this.continuationInputTarget.focus()

    } catch (error) {
      console.error("Demo error:", error)
      this.addStepToCurrentBlock("error", "Error", "error", "error", { message: error.message })

      // Remove failed message
      this.messages.pop()

      streamingContent.innerHTML = `<span class="text-red-400">Error: ${this.escapeHtml(error.message)}</span>`
    } finally {
      this.sendBtnTarget.disabled = false
      this.sendBtnTarget.textContent = "Send Request"
      if (this.hasContinuationBtnTarget) {
        this.continuationBtnTarget.disabled = false
        this.continuationBtnTarget.textContent = "Send"
      }
    }
  }

  addUserMessage(message) {
    const div = document.createElement("div")
    div.className = "flex justify-end mb-4"
    div.innerHTML = `
      <div class="bg-cyan-900/50 rounded-lg px-4 py-3 max-w-[80%]">
        <div class="text-cyan-400 text-xs font-medium mb-1">You</div>
        <div class="text-gray-200 whitespace-pre-wrap">${this.escapeHtml(message)}</div>
      </div>
    `
    this.chatContainerTarget.appendChild(div)
  }

  createStepsBlock() {
    const div = document.createElement("div")
    div.className = "mb-4 bg-slate-800/50 rounded-lg px-4 py-3 text-sm"
    div.innerHTML = `
      <div class="text-gray-500 text-xs font-medium mb-2">Processing request...</div>
      <div class="steps-container space-y-1">
        <!-- Steps will be inserted here -->
      </div>
    `
    return div
  }

  createResponseBlock() {
    const div = document.createElement("div")
    div.className = "flex justify-start mb-4"
    div.innerHTML = `
      <div class="bg-slate-800/50 rounded-lg px-4 py-3 max-w-[90%] w-full">
        <div class="text-green-400 text-xs font-medium mb-1">Assistant</div>
        <div class="streaming-content text-gray-200 whitespace-pre-wrap"></div>
        <div class="mt-3 pt-2 border-t border-slate-700 flex flex-wrap gap-4 text-xs">
          <span class="text-gray-500">Cost: <span class="text-yellow-400 cost-display">-</span> sats (<span class="text-yellow-400 cost-usd-display">-</span>)</span>
          <span class="text-gray-500">Status: <span class="text-green-400 charge-status-display">-</span></span>
        </div>
      </div>
    `
    return div
  }

  handleStreamEvent(data, streamingContent) {
    if (data.step) {
      const stepInfo = this.constructor.steps[data.step]
      if (stepInfo) {
        if (data.status === "pending") {
          this.addStepToCurrentBlock(data.step, stepInfo.label, stepInfo.icon, "pending", data)
        } else if (data.status === "complete") {
          this.updateStepInCurrentBlock(data.step, "complete", data)
        } else if (data.status === "error") {
          this.updateStepInCurrentBlock(data.step, "error", data)
        }
      }
      return
    }

    if (data.content) {
      streamingContent.textContent += data.content
      // Scroll with offset so content isn't at the very bottom edge
      const rect = streamingContent.getBoundingClientRect()
      const offset = 150
      if (rect.bottom > window.innerHeight - offset) {
        window.scrollBy({ top: rect.bottom - window.innerHeight + offset, behavior: "smooth" })
      }
    }

    if (data.code && data.message) {
      this.addStepToCurrentBlock("error", data.message, "error", "error", {})
    }
  }

  addStepToCurrentBlock(stepId, label, icon, status, data) {
    const icons = {
      wallet: '<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 10h18M7 15h1m4 0h1m-7 4h12a3 3 0 003-3V8a3 3 0 00-3-3H6a3 3 0 00-3 3v8a3 3 0 003 3z"></path></svg>',
      shield: '<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z"></path></svg>',
      calculator: '<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 7h6m0 10v-3m-3 3h.01M9 17h.01M9 14h.01M12 14h.01M15 11h.01M12 11h.01M9 11h.01M7 21h10a2 2 0 002-2V5a2 2 0 00-2-2H7a2 2 0 00-2 2v14a2 2 0 002 2z"></path></svg>',
      receipt: '<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2"></path></svg>',
      lightning: '<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"></path></svg>',
      brain: '<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3m3.343-5.657l-.707-.707m2.828 9.9a5 5 0 117.072 0l-.548.547A3.374 3.374 0 0014 18.469V19a2 2 0 11-4 0v-.531c0-.895-.356-1.754-.988-2.386l-.548-.547z"></path></svg>',
      refund: '<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 10h10a8 8 0 018 8v2M3 10l6 6m-6-6l6-6"></path></svg>',
      error: '<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"></path></svg>'
    }

    const statusColors = {
      pending: "text-yellow-400",
      complete: "text-green-400",
      error: "text-red-400"
    }

    let details = ""
    if (data.estimated_sats) details = `(est. ${data.estimated_sats} sats)`
    if (data.charge_sats) details = `(${data.charge_sats} sats)`
    if (data.amount_sats) details = `(${data.amount_sats} sats)`

    const div = document.createElement("div")
    div.id = `current-step-${stepId}`
    div.className = `flex items-center gap-2 ${statusColors[status]}`
    div.innerHTML = `
      <span class="flex-shrink-0">${icons[icon] || icons.error}</span>
      <span class="flex-grow">${label}</span>
      <span class="text-xs opacity-75">${details}</span>
      ${status === "pending" ? '<span class="flex-shrink-0"><svg class="animate-spin h-3 w-3" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg></span>' : ""}
    `

    this.currentStepsContainer.appendChild(div)
    this.currentStepElements[stepId] = div
  }

  updateStepInCurrentBlock(stepId, status, data) {
    const div = this.currentStepElements[stepId]
    if (!div) return

    const statusColors = {
      pending: "text-yellow-400",
      complete: "text-green-400",
      error: "text-red-400"
    }

    div.className = `flex items-center gap-2 ${statusColors[status]}`

    let details = ""
    if (data.cost_sats !== undefined) details = `(${data.cost_sats} sats / $${(data.cost_usd || 0).toFixed(6)})`
    if (data.refund_sats !== undefined && stepId === "refund") details = `(${data.refund_sats} sats)`
    if (data.amount_sats !== undefined && stepId !== "cost_calculate") details = `(${data.amount_sats} sats)`

    const spans = div.querySelectorAll("span")
    if (spans.length >= 3) {
      spans[2].textContent = details
    }

    const spinner = div.querySelector(".animate-spin")
    if (spinner) {
      const parent = spinner.parentElement
      if (status === "complete") {
        parent.innerHTML = '<svg class="w-3 h-3 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path></svg>'
      } else if (status === "error") {
        parent.innerHTML = '<svg class="w-3 h-3 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path></svg>'
      }
    }
  }

  escapeHtml(text) {
    const div = document.createElement('div')
    div.textContent = text
    return div.innerHTML
  }
}
