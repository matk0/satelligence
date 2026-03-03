Rails.application.routes.draw do
  root "pages#home"

  get "docs", to: "pages#docs"

  # API proxy to Go backend
  scope "/api" do
    post "chat", to: "api#chat"
    post "chat/stream", to: "api#chat_stream"
    get "models", to: "api#models"
  end

  # Health check for Docker/load balancer
  get "up" => "rails/health#show", as: :rails_health_check
end
