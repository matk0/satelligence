import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = [
    "nwcInput",
    "messageInput",
    "modelSelect",
    "sendBtn",
    "timeline", "timelineItems",
    "response", "responseContent",
    "costDisplay", "refundDisplay"
  ]

  connect() {
    this.loadModels()
  }

  async loadModels() {
    try {
      const response = await fetch("/api/models")
      if (!response.ok) {
        throw new Error("Failed to fetch models")
      }
      const data = await response.json()
      const models = data.models || []

      // Clear existing options
      this.modelSelectTarget.innerHTML = ""

      // Add model options
      models.forEach((model, index) => {
        const option = document.createElement("option")
        option.value = model
        option.textContent = model
        // Select first model by default
        if (index === 0) {
          option.selected = true
        }
        this.modelSelectTarget.appendChild(option)
      })

      // Fallback if no models returned
      if (models.length === 0) {
        const option = document.createElement("option")
        option.value = "gpt-4o-mini"
        option.textContent = "gpt-4o-mini"
        this.modelSelectTarget.appendChild(option)
      }
    } catch (error) {
      console.error("Failed to load models:", error)
      // Fallback to default model
      this.modelSelectTarget.innerHTML = '<option value="gpt-4o-mini">gpt-4o-mini</option>'
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

    // Disable button
    this.sendBtnTarget.disabled = true
    this.sendBtnTarget.textContent = "Processing..."

    // Show timeline
    this.timelineTarget.classList.remove("hidden")
    this.timelineItemsTarget.innerHTML = ""

    // Hide previous response
    this.responseTarget.classList.add("hidden")

    try {
      // Step 1: Estimating cost
      this.addTimelineItem("clock", "Estimating cost...", "pending")
      await this.delay(300)
      this.updateTimelineItem(0, "check", "Cost estimated: ~2 sats", "complete")

      // Step 2: Requesting payment
      this.addTimelineItem("lightning", "Requesting payment via NWC...", "pending")

      // Make the actual API call
      const response = await fetch("/api/nwc/chat", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-NWC": nwc
        },
        body: JSON.stringify({
          model: model,
          messages: [{ role: "user", content: message }],
          max_tokens: 500
        })
      })

      if (!response.ok) {
        const error = await response.json()
        throw new Error(error.error?.message || error.message || "Request failed")
      }

      this.updateTimelineItem(1, "check", "Payment confirmed!", "complete")

      // Step 3: Calling AI
      this.addTimelineItem("brain", "Calling AI model...", "pending")

      const data = await response.json()

      this.updateTimelineItem(2, "check", "Response received!", "complete")

      // Step 4: Processing refund
      const chargedSats = response.headers.get("X-Charged-Sats") || "?"
      const costSats = response.headers.get("X-Cost-Sats") || "?"
      const refundSats = response.headers.get("X-Refund-Sats") || "0"

      if (parseInt(refundSats) > 0) {
        this.addTimelineItem("refund", `Refunding ${refundSats} sats...`, "pending")
        await this.delay(500)
        this.updateTimelineItem(3, "check", `Refunded ${refundSats} sats!`, "complete")
      }

      // Show response
      this.responseTarget.classList.remove("hidden")
      this.responseContentTarget.textContent = data.choices?.[0]?.message?.content || "No response"
      this.costDisplayTarget.textContent = costSats
      this.refundDisplayTarget.textContent = refundSats

    } catch (error) {
      console.error("Demo error:", error)
      this.addTimelineItem("error", `Error: ${error.message}`, "error")
    } finally {
      this.sendBtnTarget.disabled = false
      this.sendBtnTarget.textContent = "Send Request"
    }
  }

  addTimelineItem(icon, text, status) {
    const icons = {
      clock: "&#x23F3;",
      lightning: "&#x26A1;",
      brain: "&#x1F9E0;",
      refund: "&#x1F4B8;",
      check: "&#x2705;",
      error: "&#x274C;"
    }

    const colors = {
      pending: "text-yellow-400",
      complete: "text-green-400",
      error: "text-red-400"
    }

    const div = document.createElement("div")
    div.className = `flex items-center gap-3 ${colors[status]}`
    div.innerHTML = `
      <span class="text-lg">${icons[icon]}</span>
      <span>${text}</span>
      ${status === "pending" ? '<span class="animate-pulse">...</span>' : ""}
    `
    this.timelineItemsTarget.appendChild(div)
  }

  updateTimelineItem(index, icon, text, status) {
    const icons = {
      clock: "&#x23F3;",
      lightning: "&#x26A1;",
      brain: "&#x1F9E0;",
      refund: "&#x1F4B8;",
      check: "&#x2705;",
      error: "&#x274C;"
    }

    const colors = {
      pending: "text-yellow-400",
      complete: "text-green-400",
      error: "text-red-400"
    }

    const items = this.timelineItemsTarget.children
    if (items[index]) {
      items[index].className = `flex items-center gap-3 ${colors[status]}`
      items[index].innerHTML = `
        <span class="text-lg">${icons[icon]}</span>
        <span>${text}</span>
      `
    }
  }

  delay(ms) {
    return new Promise(resolve => setTimeout(resolve, ms))
  }
}
