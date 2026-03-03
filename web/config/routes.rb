Rails.application.routes.draw do
  root "pages#home"

  get "docs", to: "pages#docs"

  # Machine-readable manifest for AI agents
  get ".well-known/ai-agent.json", to: "pages#ai_agent_manifest"

  # API proxy to Go backend
  scope "/api" do
    post "chat", to: "api#chat"
    post "chat/stream", to: "api#chat_stream"
    get "models", to: "api#models"
  end

  # Health check for Docker/load balancer
  get "up" => "rails/health#show", as: :rails_health_check
end
