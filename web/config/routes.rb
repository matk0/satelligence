Rails.application.routes.draw do
  root "pages#home"

  get "docs", to: "pages#docs"

  # API proxy to Go backend
  scope "/api" do
    post "webln/quote", to: "api#webln_quote"
    post "webln/chat", to: "api#webln_chat"
    post "webln/chat/stream", to: "api#webln_chat_stream"
    post "webln/refund", to: "api#webln_refund"
    post "nwc/chat", to: "api#nwc_chat"
    post "nwc/chat/stream", to: "api#nwc_chat_stream"
    get "models", to: "api#models"
  end

  # Health check for Docker/load balancer
  get "up" => "rails/health#show", as: :rails_health_check
end
