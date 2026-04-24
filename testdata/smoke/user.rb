class User < ApplicationRecord
  has_many :orders
  include Trackable

  def full_name
  end
end
