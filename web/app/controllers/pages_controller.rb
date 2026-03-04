class PagesController < ApplicationController
  def home
  end

  def docs
  end

  def ai_agent_manifest
    manifest = {
      name: "Trandor",
      version: "1.0.0",
      description: "Bitcoin-native AI API. Agents create wallets, humans fund them, agents spend sats for inference.",
      base_url: "https://api.trandor.com",
      docs_url: "https://www.trandor.com/docs",

      # CRITICAL: This is what agents should follow to onboard
      onboarding: {
        summary: "Create wallet → Get funded → Use API",
        steps: [
          {
            step: 1,
            action: "Create your wallet",
            method: "POST",
            url: "https://api.trandor.com/v1/wallet/create",
            headers: { "Content-Type": "application/json" },
            body: { name: "my-agent-wallet" },
            auth_required: false,
            note: "No authentication needed. Save the response - you need wallet_id, nwc_connection_string, and invoice_key.",
            response_fields: ["wallet_id", "nwc_connection_string", "admin_key", "invoice_key"]
          },
          {
            step: 2,
            action: "Create deposit invoice",
            method: "POST",
            url: "https://api.trandor.com/v1/wallet/{wallet_id}/deposit",
            headers: { "Content-Type": "application/json", "X-Invoice-Key": "{invoice_key from step 1}" },
            body: { amount_sats: 10000, memo: "Fund my AI agent" },
            auth_required: true,
            auth_header: "X-Invoice-Key",
            note: "10000 sats ≈ $1. Returns a Lightning invoice (payment_request).",
            response_fields: ["payment_request", "payment_hash"]
          },
          {
            step: 3,
            action: "Ask human to pay the invoice",
            requires_human: true,
            note: "Present the payment_request (Lightning invoice) to your human operator. They pay it from any Lightning wallet. Wait for confirmation.",
            human_instruction: "Please pay this Lightning invoice to fund the agent's wallet: {payment_request}"
          },
          {
            step: 4,
            action: "Verify funding (optional)",
            method: "GET",
            url: "https://api.trandor.com/v1/wallet/{wallet_id}",
            headers: { "X-Admin-Key": "{admin_key from step 1}" },
            auth_required: true,
            auth_header: "X-Admin-Key",
            note: "Check that balance_sats > 0 before proceeding."
          },
          {
            step: 5,
            action: "Use the API",
            method: "POST",
            url: "https://api.trandor.com/v1/chat/completions",
            headers: {
              "Content-Type": "application/json",
              "Authorization": "Bearer {nwc_connection_string from step 1}"
            },
            body: {
              model: "gpt-5.2",
              messages: [{ role: "user", content: "Hello!" }]
            },
            auth_required: true,
            auth_header: "Authorization",
            auth_format: "Bearer {nwc_connection_string}",
            note: "You now have AI API access. Cost is charged per request from your wallet balance."
          }
        ]
      },

      endpoints: {
        wallet_create: {
          method: "POST",
          path: "/v1/wallet/create",
          description: "Create a hosted wallet. Returns NWC connection string. NO AUTH REQUIRED.",
          auth_required: false,
          request_body: {
            name: { type: "string", required: false, description: "Optional wallet name" }
          },
          response: {
            wallet_id: "string",
            nwc_connection_string: "string - USE THIS AS YOUR API KEY",
            admin_key: "string - for checking balance",
            invoice_key: "string - for creating deposit invoices"
          }
        },
        wallet_deposit: {
          method: "POST",
          path: "/v1/wallet/{wallet_id}/deposit",
          description: "Create Lightning invoice for human to pay.",
          auth_required: true,
          auth_header: "X-Invoice-Key",
          request_body: {
            amount_sats: { type: "integer", required: true, description: "Amount in satoshis (10000 ≈ $1)" },
            memo: { type: "string", required: false, description: "Invoice memo" }
          },
          response: {
            payment_request: "string - Lightning invoice for human to pay",
            payment_hash: "string"
          }
        },
        wallet_info: {
          method: "GET",
          path: "/v1/wallet/{wallet_id}",
          description: "Check wallet balance.",
          auth_required: true,
          auth_header: "X-Admin-Key",
          response: {
            wallet_id: "string",
            balance_sats: "integer"
          }
        },
        models: {
          method: "GET",
          path: "/v1/models",
          description: "List available models.",
          auth_required: false
        },
        chat_completions: {
          method: "POST",
          path: "/v1/chat/completions",
          description: "Create chat completion. OpenAI-compatible.",
          auth_required: true,
          auth_header: "Authorization",
          auth_format: "Bearer {nwc_connection_string}"
        }
      },

      auth: {
        type: "nwc",
        description: "Your nwc_connection_string from wallet creation IS your API key",
        header: "Authorization",
        format: "Bearer nostr+walletconnect://...",
        how_to_get: "POST /v1/wallet/create - no auth required, returns nwc_connection_string"
      },

      models: ["gpt-5.2"],

      pricing: {
        model: "pay-per-request",
        minimum_balance: "$0.50 (≈735 sats)",
        markup: "5% over OpenAI prices",
        note: "Actual cost charged after response. Check X-Cost-Sats header."
      },

      response_headers: {
        "X-Cost-Sats": "Actual cost in satoshis",
        "X-Cost-USD": "Actual cost in USD",
        "X-Charge-Status": "success, pending, or failed"
      }
    }

    render json: manifest
  end
end
