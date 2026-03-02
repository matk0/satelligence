Rails.application.routes.draw do
  root "pages#home"

  get "docs", to: "pages#docs"
  get "chat", to: "chat#index"

  # API proxy to Go backend
  scope "/api" do
    post "webln/quote", to: "api#webln_quote"
    post "webln/chat", to: "api#webln_chat"
    post "webln/chat/stream", to: "api#webln_chat_stream"
    post "webln/refund", to: "api#webln_refund"
    post "nwc/chat", to: "api#nwc_chat"
    get "models", to: "api#models"
  end

  # Health check for Docker/load balancer
  get "up" => "rails/health#show", as: :rails_health_check
end
