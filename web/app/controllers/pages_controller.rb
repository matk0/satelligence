class PagesController < ApplicationController
  def home
  end

  def docs
  end

  def ai_agent_manifest
    manifest = {
      name: "Trandor",
      version: "1.0.0",
      description: "Bitcoin-native, OpenAI-compatible AI inference API. Pay per request with Lightning via NWC (Nostr Wallet Connect). No accounts, no API keys.",
      base_url: "https://api.trandor.com",
      docs_url: "https://www.trandor.com/docs",

      endpoints: {
        models: {
          method: "GET",
          path: "/v1/models",
          description: "List available models. No authentication required.",
          auth_required: false
        },
        chat_completions: {
          method: "POST",
          path: "/v1/chat/completions",
          description: "Create chat completion. Returns full response.",
          auth_required: true
        },
        chat_completions_stream: {
          method: "POST",
          path: "/v1/chat/completions/stream",
          description: "Create chat completion with Server-Sent Events streaming.",
          auth_required: true
        },
        wallet_create: {
          method: "POST",
          path: "/v1/wallet/create",
          description: "Create a hosted wallet. Returns NWC connection string for autonomous agents.",
          auth_required: false,
          request_body: {
            name: { type: "string", required: false, description: "Optional wallet name" }
          },
          response: {
            wallet_id: "string",
            nwc_connection_string: "string (use in X-NWC header)",
            admin_key: "string (for wallet management)",
            invoice_key: "string (for creating deposit invoices)"
          }
        },
        wallet_info: {
          method: "GET",
          path: "/v1/wallet/{wallet_id}",
          description: "Get wallet info and balance.",
          auth_required: true,
          auth_header: "X-Admin-Key",
          response: {
            wallet_id: "string",
            balance_sats: "integer"
          }
        },
        wallet_deposit: {
          method: "POST",
          path: "/v1/wallet/{wallet_id}/deposit",
          description: "Create a Lightning invoice to deposit funds into the wallet.",
          auth_required: true,
          auth_header: "X-Invoice-Key",
          request_body: {
            amount_sats: { type: "integer", required: true, description: "Amount in satoshis" },
            memo: { type: "string", required: false, description: "Invoice memo" }
          },
          response: {
            payment_request: "string (Lightning invoice)",
            payment_hash: "string"
          }
        }
      },

      auth: {
        type: "nwc",
        description: "Nostr Wallet Connect - Lightning payment serves as authentication",
        header: "X-NWC",
        format: "nostr+walletconnect://WALLET_PUBKEY?relay=wss://relay.example.com&secret=SECRET",
        setup_guide: "https://www.trandor.com/docs#nwc-setup-guide",
        requirements: [
          "NWC connection string with pay_invoice permission",
          "Sufficient balance for 2x estimated request cost (refund issued after)"
        ],
        options: [
          {
            name: "Bring Your Own Wallet",
            description: "Human sets up wallet (Alby, etc.) and provides NWC string to agent",
            steps: ["Human creates NWC connection in their wallet", "Human gives NWC string to agent", "Agent uses NWC string in X-NWC header"]
          },
          {
            name: "Hosted Wallet",
            description: "Agent creates wallet via API, human funds it via Lightning invoice",
            steps: ["Agent calls POST /v1/wallet/create", "Agent gets nwc_connection_string", "Agent creates deposit invoice via POST /v1/wallet/{id}/deposit", "Human pays invoice", "Agent uses nwc_connection_string in X-NWC header"]
          }
        ],
        note: "For BYOW, your human operator sets up the wallet. For hosted wallets, the agent creates the wallet and human just funds it."
      },

      request_format: {
        content_type: "application/json",
        body: {
          model: {
            type: "string",
            required: true,
            description: "Model ID from /v1/models endpoint"
          },
          messages: {
            type: "array",
            required: true,
            description: "Array of {role, content} message objects",
            example: [
              { role: "system", content: "You are a helpful assistant." },
              { role: "user", content: "Hello!" }
            ]
          },
          max_tokens: {
            type: "integer",
            required: false,
            default: 4096,
            description: "Maximum tokens in response"
          },
          temperature: {
            type: "float",
            required: false,
            default: 1.0,
            range: "0-2",
            description: "Randomness of response"
          }
        }
      },

      response_headers: {
        "X-Charged-Sats": "Amount charged upfront (2x estimate)",
        "X-Cost-Sats": "Actual cost in satoshis",
        "X-Cost-USD": "Actual cost in USD",
        "X-Refund-Sats": "Amount refunded to wallet",
        "X-Refund-Status": "success, failed, or none"
      },

      pricing: {
        unit: "satoshis",
        model: "pay-per-request",
        markup: "5% over OpenAI prices",
        note: "2x estimated cost charged upfront, difference refunded after response"
      },

      capabilities: [
        "openai-compatible",
        "streaming",
        "instant-refunds",
        "no-account-required",
        "hosted-wallets"
      ],

      integrations: {
        openai_proxy: {
          name: "trandor-proxy",
          description: "OpenAI-compatible proxy that handles NWC authentication. Use when your framework requires OPENAI_BASE_URL + OPENAI_API_KEY format.",
          repo: "https://github.com/matk0/trandor-proxy",
          skill_file: "SKILL.md"
        }
      },

      example_curl: 'curl -X POST "https://api.trandor.com/v1/chat/completions" -H "Content-Type: application/json" -H "X-NWC: nostr+walletconnect://..." -d \'{"model":"gpt-4.1","messages":[{"role":"user","content":"Hello!"}]}\''
    }

    render json: manifest
  end
end
