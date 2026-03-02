Rails.application.routes.draw do
  root "pages#home"

  get "docs", to: "pages#docs"
  get "chat", to: "chat#index"

  # Health check for Docker/load balancer
  get "up" => "rails/health#show", as: :rails_health_check
end
