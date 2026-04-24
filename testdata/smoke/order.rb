class Order < ApplicationRecord
  has_many :line_items
  belongs_to :user
  include Trackable

  before_save :validate_total

  def validate_total
  end

  def total
  end
end
