# RSpec-style describe with a constant
RSpec.describe Order do
  describe "#total" do
    it "calculates correctly" do
    end
  end
end

# Bare describe with a constant
describe Invoice do
  it "validates presence" do
  end
end

# Minitest-style test class
class UserTest < ActiveSupport::TestCase
  def test_valid
  end
end

# Nested module test class
class Admin::DashboardControllerTest < ActionDispatch::IntegrationTest
  def test_index
  end
end

# Negative cases: should NOT produce tests edges

# describe with a string (not a constant) — no tests edge
describe "some behavior" do
  it "works" do
  end
end

# describe with a string inside RSpec.describe — no tests edge
RSpec.describe "utility functions" do
  it "works" do
  end
end

# A class that inherits from something else — not a test class
class OrderProcessor < BaseProcessor
  def process
  end
end
