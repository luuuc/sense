class Order
  def save; end
  def validate; end
end

class User
  def name; end
end

class Item
  def save; end
end

class Service
  def process_orders
    orders = Order.new
    orders.each do |order|
      order.validate
      order.save
    end
  end

  def process_users
    users = User.new
    users.map { |user| user.name }
  end

  def nested_blocks
    orders = Order.new
    orders.each do |order|
      items = Item.new
      items.each do |item|
        item.save
      end
    end
  end

  def non_collection_block
    File.open("x") { |f| f.read }
  end
end
