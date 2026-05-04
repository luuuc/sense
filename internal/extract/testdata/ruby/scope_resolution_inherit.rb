module Admin
  class BaseController < ActionController::Base
    def current_admin
      Admin.find(session[:admin_id])
    end
  end

  class UsersController < BaseController
    include Searchable

    def index
      users = User.all
      render(users)
    end
  end
end

class Api::V2::OrdersController < ApplicationController
  prepend Auditable

  def create
    order = Order.new
    order.save
  end
end
