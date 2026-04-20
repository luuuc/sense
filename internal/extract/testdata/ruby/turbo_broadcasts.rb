class Order < ApplicationRecord
  broadcasts_to :store
  broadcasts
end

class Product < ApplicationRecord
  broadcasts_to :catalog
end
