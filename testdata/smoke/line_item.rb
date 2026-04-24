class LineItem < ApplicationRecord
  belongs_to :order
  include Trackable

  def subtotal
  end
end
