class LineItem < ApplicationRecord
  belongs_to :order
  include Trackable

  def subtotal
  end

  private

  # orphaned_private is genuinely dead: private, zero callers, and its name is
  # not a reflection-dispatch target. It is the one symbol in the smoke fixture
  # that must earn the `dead` verdict — the closed-world proof for a Ruby
  # private method, pinning the two-sided gate end to end.
  def orphaned_private
  end
end
