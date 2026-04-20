# Simulates config/routes.rb content.
# Route DSL calls are bare (no enclosing class), which is how the
# extractor distinguishes them from regular method calls.

resources :orders
resources :products, only: [:index, :show]
resource :session

get "/dashboard", to: "pages#home"
post "/webhooks", to: "webhooks#receive"

namespace :admin do
  resources :users
  get "/stats", to: "dashboard#index"
end

# Negative case: resources inside a class body is a regular call
class SomeService
  def setup
    resources :things
  end
end
