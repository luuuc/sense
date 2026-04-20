class OrdersController
  before_action :authenticate!
  before_action :set_order, only: [:show, :edit]
  after_action :log_request

  def index
  end

  def authenticate!
  end

  def set_order
  end

  def log_request
  end
end

class Invoice
  before_save :normalize_total
  after_commit :sync_to_search
  before_validation :strip_whitespace
  after_create :send_notification

  def normalize_total
  end

  def sync_to_search
  end

  def strip_whitespace
  end

  def send_notification
  end
end

# Negative case: callback-like call inside a method body
class NotACallback
  def setup
    before_action :something
  end

  def something
  end
end
