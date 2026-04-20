class Order
  belongs_to :user
  belongs_to :warehouse, class_name: "Fulfillment::Depot"
  has_many :line_items
  has_one :invoice
  has_and_belongs_to_many :tags
end

class Product
  has_many :categories, class_name: "ProductCategory"
  has_many :variants
end

# Negative cases: should NOT produce composes edges.

class DynamicModel
  # has_many inside a method body is a regular call, not a declaration
  def setup
    has_many :things
  end
end
